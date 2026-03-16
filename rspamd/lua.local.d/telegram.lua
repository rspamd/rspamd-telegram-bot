-- Telegram-specific detection rules for Rspamd
-- Profile tracking is handled by telegram_profile.lua
-- These rules use tg_profile:{user_id} data from Redis

local rspamd_logger = require "rspamd_logger"
local lua_redis = require "lua_redis"
local lua_util = require "lua_util"

local N = "telegram"
local redis_params

local new_user_check_id
local join_flood_id

-- Redis script: check if user is new + has URL (atomic read)
-- KEYS[1] = tg_profile:{user_id}
-- Returns: first_seen, msg_count (or nil if no profile)
local new_user_check_script = [[
local profile_key = KEYS[1]
local first_seen = redis.call('HGET', profile_key, 'first_seen')
if not first_seen then
  return nil
end
local msg_count = redis.call('HGET', profile_key, 'msg_count') or '0'
return {first_seen, msg_count}
]]

-- Redis script: increment join counter and return count
-- KEYS[1] = tg_join_count:{chat_id}
-- ARGV[1] = ttl
local join_flood_script = [[
local key = KEYS[1]
local ttl = tonumber(ARGV[1])
redis.call('INCR', key)
redis.call('EXPIRE', key, ttl)
return redis.call('GET', key)
]]

-- Init
redis_params = lua_redis.parse_redis_server(N)
if not redis_params then
  rspamd_logger.errx(rspamd_config, '%s: no redis servers defined, disabling', N)
  return
end

new_user_check_id = lua_redis.add_redis_script(new_user_check_script, redis_params)
join_flood_id = lua_redis.add_redis_script(join_flood_script, redis_params)

local function get_header(task, name)
  return task:get_header(name)
end

local function has_urls(task)
  local urls = task:get_urls()
  return urls and #urls > 0
end

-- TELEGRAM_NEW_USER_LINK: new/recent user posting URLs
rspamd_config:register_symbol({
  name = 'TELEGRAM_NEW_USER_LINK',
  score = 6.0,
  group = 'telegram',
  description = 'New user posting URLs',
  callback = function(task)
    local user_id = get_header(task, 'X-Telegram-User-Id')
    if not user_id then return false end
    if not has_urls(task) then return false end

    local profile_key = string.format("tg_profile:%s", user_id)

    lua_redis.exec_redis_script(new_user_check_id,
      { task = task, is_write = false },
      function(err, data)
        if err then
          rspamd_logger.errx(task, '%s: new user check failed: %s', N, err)
          return
        end

        if not data or type(data) ~= 'table' then
          -- No profile = first message ever with a URL
          task:insert_result('TELEGRAM_NEW_USER_LINK', 1.0, 'first message with URL')
          return
        end

        local first_seen = tonumber(data[1])
        if not first_seen then return end
        local age = os.time() - first_seen

        if age < 86400 then
          task:insert_result('TELEGRAM_NEW_USER_LINK', 1.0,
            string.format('user age: %d min', math.floor(age / 60)))
        end
      end,
      { profile_key },
      {}
    )

    return false -- async
  end,
})

-- TELEGRAM_FORWARD_SPAM: forward from unknown user with links
rspamd_config:register_symbol({
  name = 'TELEGRAM_FORWARD_SPAM',
  score = 4.0,
  group = 'telegram',
  description = 'Forwarded message from unknown user with links',
  callback = function(task)
    local is_forward = get_header(task, 'X-Telegram-Is-Forward')
    if is_forward ~= 'true' then return false end
    if not has_urls(task) then return false end

    return true, 1.0, 'forwarded message with URLs'
  end,
})

-- TELEGRAM_JOIN_FLOOD: multiple joins in short period
rspamd_config:register_symbol({
  name = 'TELEGRAM_JOIN_FLOOD',
  score = 3.0,
  group = 'telegram',
  description = 'Multiple joins in short period',
  callback = function(task)
    local msg_type = get_header(task, 'X-Telegram-Message-Type')
    if msg_type ~= 'join' then return false end

    local chat_id = get_header(task, 'X-Telegram-Chat-Id')
    if not chat_id then return false end

    local key = string.format("tg_join_count:%s", chat_id)

    lua_redis.exec_redis_script(join_flood_id,
      { task = task, is_write = true },
      function(err, data)
        if err then
          rspamd_logger.errx(task, '%s: join flood check failed: %s', N, err)
          return
        end
        local count = tonumber(data)
        if count and count > 5 then
          task:insert_result('TELEGRAM_JOIN_FLOOD', 1.0,
            string.format('%d joins recently', count))
        end
      end,
      { key },
      { '300' }  -- 5 minute TTL
    )

    return false -- async
  end,
})

-- TELEGRAM_SUSPICIOUS_NAME: display name matches spam patterns
rspamd_config:register_symbol({
  name = 'TELEGRAM_SUSPICIOUS_NAME',
  score = 3.0,
  group = 'telegram',
  description = 'Display name matches spam patterns',
  callback = function(task)
    local first_name = get_header(task, 'X-Telegram-First-Name') or ''
    local last_name = get_header(task, 'X-Telegram-Last-Name') or ''
    local full_name = (first_name .. ' ' .. last_name):lower()

    local suspicious_patterns = {
      'crypto', 'bitcoin', 'invest', 'trading', 'forex',
      'profit', 'binance', 'earn money', 'airdrop', 'nft',
      'admin', 'support', 'helpdesk', 'moderator',
    }

    for _, pattern in ipairs(suspicious_patterns) do
      if full_name:find(pattern, 1, true) then
        return true, 1.0, pattern
      end
    end

    return false
  end,
})

-- TELEGRAM_CRYPTO_SPAM: crypto/investment spam patterns in text
rspamd_config:register_symbol({
  name = 'TELEGRAM_CRYPTO_SPAM',
  score = 7.0,
  group = 'telegram',
  description = 'Crypto/investment spam patterns',
  callback = function(task)
    local content = task:get_content()
    if not content then return false end

    local text = tostring(content):lower()

    local crypto_patterns = {
      'guaranteed profit',
      'daily returns',
      'invest.*minimum',
      'bitcoin.*opportunity',
      'crypto.*signal',
      'forex.*trading.*group',
      'earn.*%d+.*daily',
      'whatsapp.*%+%d',
      'telegram.*t%.me',
      'mining.*pool',
      'wallet.*connect',
    }

    local matches = {}
    for _, pattern in ipairs(crypto_patterns) do
      if text:find(pattern) then
        table.insert(matches, pattern)
      end
    end

    if #matches > 0 then
      return true, math.min(#matches / 2, 1.0), table.concat(matches, ', ')
    end

    return false
  end,
})

-- TELEGRAM_SHORT_FIRST_MSG: very short first message from new user with link
rspamd_config:register_symbol({
  name = 'TELEGRAM_SHORT_FIRST_MSG',
  score = 4.0,
  group = 'telegram',
  description = 'Short first message from new user with link',
  callback = function(task)
    local user_id = get_header(task, 'X-Telegram-User-Id')
    if not user_id then return false end
    if not has_urls(task) then return false end

    local content = task:get_content()
    if not content then return false end
    local text = tostring(content)
    if #text > 100 then return false end

    local profile_key = string.format("tg_profile:%s", user_id)

    lua_redis.exec_redis_script(new_user_check_id,
      { task = task, is_write = false },
      function(err, data)
        if err then return end

        if not data or type(data) ~= 'table' then
          -- No profile = brand new user
          task:insert_result('TELEGRAM_SHORT_FIRST_MSG', 1.0, 'short first message with URL')
          return
        end

        local msg_count = tonumber(data[2]) or 0
        if msg_count <= 2 then
          task:insert_result('TELEGRAM_SHORT_FIRST_MSG', 1.0, 'short first message with URL')
        end
      end,
      { profile_key },
      {}
    )

    return false -- async
  end,
})
