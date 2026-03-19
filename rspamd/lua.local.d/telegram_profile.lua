-- Telegram user profiling and reputation scoring
--
-- Redis keys:
--   tg_profile:{user_id}           HASH  (first_seen, last_seen, msg_count, username, first_name, last_name, admin_replies)
--   tg_profile:{user_id}:messages  LIST  (last N message texts, most recent first)
--   tg_profile:{user_id}:contacts  SET   (user IDs this user interacted with)
--   tg_profile:{user_id}:channels  SET   (chat IDs this user has been seen in)
--   tg_username:{username}         STRING -> user_id  (reverse lookup for /user command)
--   tg_channels                    ZSET  chat_id -> last_activity_timestamp
--   tg_channel:{chat_id}:info      HASH  (title, msg_count)
--   tg_channel:{chat_id}:users     ZSET  user_id -> msg_count_in_channel

local rspamd_logger = require "rspamd_logger"
local lua_redis = require "lua_redis"
local lua_util = require "lua_util"
local lua_mime = require "lua_mime"

local N = "telegram_profile"
local redis_params

local PROFILE_TTL = 90 * 86400  -- 90 days
local MAX_MESSAGES = 10
local MAX_MSG_LEN = 200

local profile_update_id
local reputation_check_id

-- Redis script: atomic profile update
-- KEYS[1] = profile_key (tg_profile:{user_id})
-- ARGV = now, username, first_name, last_name, ttl, msg_text, max_messages,
--        chat_id, chat_title, reply_to_user, is_admin
local profile_update_script = [[
local profile_key = KEYS[1]
local now = ARGV[1]
local username = ARGV[2]
local first_name = ARGV[3]
local last_name = ARGV[4]
local ttl = tonumber(ARGV[5])
local msg_text = ARGV[6]
local max_messages = tonumber(ARGV[7])
local chat_id = ARGV[8]
local chat_title = ARGV[9]
local reply_to_user = ARGV[10]
local is_admin = ARGV[11]

-- Update profile
redis.call('HSET', profile_key,
  'last_seen', now,
  'username', username,
  'first_name', first_name,
  'last_name', last_name)
redis.call('HSETNX', profile_key, 'first_seen', now)
redis.call('HINCRBY', profile_key, 'msg_count', 1)
redis.call('EXPIRE', profile_key, ttl)

local user_id = string.match(profile_key, 'tg_profile:(.+)')

-- Store last N messages
if msg_text ~= '' then
  local msg_key = profile_key .. ':messages'
  redis.call('LPUSH', msg_key, msg_text)
  redis.call('LTRIM', msg_key, 0, max_messages - 1)
  redis.call('EXPIRE', msg_key, ttl)
end

-- Reverse username lookup
if username ~= '' then
  redis.call('SET', 'tg_username:' .. string.lower(username), user_id, 'EX', ttl)
end

-- Channel tracking
if chat_id ~= '' then
  local chan_key = profile_key .. ':channels'
  redis.call('SADD', chan_key, chat_id)
  redis.call('EXPIRE', chan_key, ttl)

  redis.call('ZADD', 'tg_channels', now, chat_id)

  local chan_info = 'tg_channel:' .. chat_id .. ':info'
  redis.call('HSET', chan_info, 'title', chat_title, 'last_seen', now)
  redis.call('HINCRBY', chan_info, 'msg_count', 1)
  redis.call('EXPIRE', chan_info, ttl)

  local chan_users = 'tg_channel:' .. chat_id .. ':users'
  redis.call('ZINCRBY', chan_users, 1, user_id)
  redis.call('EXPIRE', chan_users, ttl)
end

-- Reply tracking: contacts + admin endorsement
if reply_to_user ~= '' and reply_to_user ~= '0' then
  local my_contacts = profile_key .. ':contacts'
  local their_contacts = 'tg_profile:' .. reply_to_user .. ':contacts'

  redis.call('SADD', my_contacts, reply_to_user)
  redis.call('EXPIRE', my_contacts, ttl)
  redis.call('SADD', their_contacts, user_id)
  redis.call('EXPIRE', their_contacts, ttl)

  if is_admin == 'true' then
    redis.call('HINCRBY', 'tg_profile:' .. reply_to_user, 'admin_replies', 1)
  end
end

return 1
]]

-- Redis script: fetch profile for reputation check
-- KEYS[1] = profile_key
-- Returns: {first_seen, msg_count, admin_replies, quiz_result} or nil
local reputation_check_script = [[
local profile_key = KEYS[1]
local first_seen = redis.call('HGET', profile_key, 'first_seen')
if not first_seen then
  return nil
end
local msg_count = redis.call('HGET', profile_key, 'msg_count') or '0'
local admin_replies = redis.call('HGET', profile_key, 'admin_replies') or '0'
local quiz_result = redis.call('HGET', profile_key, 'quiz_result') or ''
return {first_seen, msg_count, admin_replies, quiz_result}
]]

-- Init: parse redis params and register scripts
redis_params = lua_redis.parse_redis_server(N)
if not redis_params then
  rspamd_logger.errx(rspamd_config, '%s: no redis servers defined, disabling', N)
  return
end

profile_update_id = lua_redis.add_redis_script(profile_update_script, redis_params)
reputation_check_id = lua_redis.add_redis_script(reputation_check_script, redis_params)

local function get_header(task, name)
  return task:get_header(name)
end

-- Update user profile on every message (postfilter, runs after scoring)
rspamd_config:register_symbol({
  name = 'TELEGRAM_PROFILE_UPDATE',
  score = 0.0,
  group = 'telegram',
  type = 'postfilter',
  priority = 5,
  description = 'Update Telegram user profile in Redis',
  callback = function(task)
    -- Skip profile updates for read-only checks (e.g. /check command)
    if task:get_request_header('X-Telegram-Readonly') then
      lua_util.debugm(N, task, 'skip profile update: readonly mode')
      return false
    end

    local user_id = get_header(task, 'X-Telegram-User-Id')
    if not user_id then return false end

    local username = get_header(task, 'X-Telegram-Username') or ''
    local first_name = get_header(task, 'X-Telegram-First-Name') or ''
    local last_name = get_header(task, 'X-Telegram-Last-Name') or ''
    local is_admin = get_header(task, 'X-Telegram-Is-Admin') or 'false'
    local reply_to_user = get_header(task, 'X-Telegram-Reply-To-User-Id') or ''
    local chat_id = get_header(task, 'X-Telegram-Chat-Id') or ''
    local chat_title = get_header(task, 'X-Telegram-Chat-Title') or ''

    local now = tostring(os.time())
    local profile_key = string.format("tg_profile:%s", user_id)

    local msg_text = ''
    local sel_part = lua_mime.get_displayed_text_part(task)
    if sel_part then
      local t = sel_part:get_content()
      if t then
        msg_text = tostring(t)
      end
    end
    if msg_text == '' then
      msg_text = task:get_header('Subject') or ''
    end
    if #msg_text > MAX_MSG_LEN then
      msg_text = msg_text:sub(1, MAX_MSG_LEN)
    end

    lua_redis.exec_redis_script(profile_update_id,
      { task = task, is_write = true },
      function(err, _)
        if err then
          rspamd_logger.errx(task, '%s: profile update failed for user %s: %s',
            N, user_id, err)
        else
          lua_util.debugm(N, task, 'updated profile for user %s in chat %s',
            user_id, chat_id)
        end
      end,
      { profile_key },
      { now, username, first_name, last_name,
        tostring(PROFILE_TTL), msg_text, tostring(MAX_MESSAGES),
        chat_id, chat_title, reply_to_user, is_admin }
    )

    -- Store GPT verdict in profile if available (set by gpt.lua via mempool)
    local gpt_json = task:get_mempool():get_variable('gpt_result')
    if gpt_json then
      local rp = lua_redis.parse_redis_server(N)
      if rp then
        lua_redis.redis_make_request(task, rp, profile_key, true,
          function(err, _)
            if err then
              rspamd_logger.errx(task, '%s: failed to store GPT verdict for %s: %s',
                N, user_id, err)
            else
              lua_util.debugm(N, task, 'stored GPT verdict for user %s', user_id)
            end
          end,
          'HSET', { profile_key, 'gpt_verdict', gpt_json }
        )
      end
    end

    return false
  end,
})

-- Calculate reputation score (prefilter: runs before other checks,
-- allows enabling heavy checks like GPT for first-time users)
rspamd_config:register_symbol({
  name = 'TELEGRAM_USER_REPUTATION',
  score = -1.0,
  group = 'telegram',
  type = 'prefilter',
  priority = 10,
  description = 'User reputation based on activity history',
  callback = function(task)
    local user_id = get_header(task, 'X-Telegram-User-Id')
    if not user_id then return false end

    local profile_key = string.format("tg_profile:%s", user_id)

    lua_redis.exec_redis_script(reputation_check_id,
      { task = task, is_write = false },
      function(err, data)
        if err then
          rspamd_logger.errx(task, '%s: reputation check failed for user %s: %s',
            N, user_id, err)
          return
        end

        if not data or type(data) ~= 'table' or #data < 3 then
          -- New user, no profile yet
          lua_util.debugm(N, task, 'no profile for user %s, first-time user', user_id)
          return
        end

        local first_seen = tonumber(data[1])
        if not first_seen then return end
        local msg_count = tonumber(data[2]) or 0
        local admin_replies = tonumber(data[3]) or 0
        local quiz_result = data[4] and tostring(data[4]) or ''
        local age_days = (os.time() - first_seen) / 86400

        -- Quiz failed → spam signal (separate symbol)
        if quiz_result == 'fail' then
          task:insert_result('TELEGRAM_QUIZ_FAILED', 1.0, 'quiz verification failed')
        end

        local weight = 0.0

        if age_days >= 30 and msg_count >= 100 then
          weight = 5.0  -- established user: -5.0
        elseif msg_count >= 50 then
          weight = 4.0  -- high volume user (any age): -4.0
        elseif age_days >= 7 and msg_count >= 20 then
          weight = 3.0  -- active user: -3.0
        elseif msg_count >= 10 then
          weight = 2.0  -- regular user (any age): -2.0
        elseif age_days >= 1 and msg_count >= 5 then
          weight = 1.0  -- known user: -1.0
        end

        -- Admin endorsement: -1.0 per reply, capped at -3.0
        local admin_bonus = math.min(admin_replies, 3)
        weight = weight + admin_bonus

        -- Quiz passed: +2.0 bonus
        if quiz_result == 'pass' then
          weight = weight + 2
        end

        if weight > 0 then
          task:insert_result('TELEGRAM_USER_REPUTATION', weight,
            string.format('age:%.0fd msgs:%d admin_ok:%d quiz:%s',
              age_days, msg_count, admin_replies, quiz_result ~= '' and quiz_result or 'none'))
        end
      end,
      { profile_key },
      {}
    )

    return false -- async
  end,
})
