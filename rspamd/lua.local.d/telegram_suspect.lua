-- Telegram suspicious profile detector
-- Analyzes username, display name, and user metadata for spam indicators.
-- Only runs for new users (no reputation).

local rspamd_logger = require "rspamd_logger"
local lua_util = require "lua_util"

local N = "telegram_suspect"

local function get_header(task, name)
  return task:get_header(name)
end

-- Calculate Shannon entropy of a string (measures randomness)
local function entropy(s)
  if not s or #s == 0 then return 0 end
  local freq = {}
  for i = 1, #s do
    local c = s:sub(i, i)
    freq[c] = (freq[c] or 0) + 1
  end
  local ent = 0
  local len = #s
  for _, count in pairs(freq) do
    local p = count / len
    ent = ent - p * math.log(p) / math.log(2)
  end
  return ent
end

-- Check if string contains suspicious emoji (common in spam profiles)
local spam_emoji = {
  '\xf0\x9f\xa6\x8b', -- butterfly 🦋
  '\xe2\x9d\xa4',     -- heart ❤
  '\xf0\x9f\x92\x8b', -- kiss 💋
  '\xf0\x9f\x8c\xb8', -- cherry blossom 🌸
  '\xf0\x9f\x8c\xba', -- hibiscus 🌺
  '\xf0\x9f\x8c\xb9', -- rose 🌹
  '\xf0\x9f\x92\x95', -- two hearts 💕
  '\xf0\x9f\x92\x96', -- sparkling heart 💖
  '\xf0\x9f\x92\x9e', -- revolving hearts 💞
  '\xe2\x9c\xa8',     -- sparkles ✨
  '\xf0\x9f\x94\xa5', -- fire 🔥
  '\xf0\x9f\x92\xab', -- dizzy 💫
  '\xf0\x9f\x92\x8e', -- gem 💎
  '\xf0\x9f\x91\x91', -- crown 👑
  '\xf0\x9f\xa7\x9a',  -- elf 🧚
}

local function count_spam_emoji(s)
  if not s then return 0 end
  local count = 0
  for _, emoji in ipairs(spam_emoji) do
    if s:find(emoji, 1, true) then
      count = count + 1
    end
  end
  return count
end

-- Check if username looks random (high entropy, digits mixed with letters)
local function is_random_username(username)
  if not username or #username < 5 then return false, 0 end

  local lower = username:lower()

  -- Count character types
  local digits = 0
  local letters = 0
  local underscores = 0
  for i = 1, #lower do
    local c = lower:sub(i, i)
    if c:match('%d') then
      digits = digits + 1
    elseif c:match('%a') then
      letters = letters + 1
    elseif c == '_' then
      underscores = underscores + 1
    end
  end

  local total = digits + letters
  if total < 5 then return false, 0 end

  local score = 0

  -- High digit ratio in username is suspicious
  local digit_ratio = digits / total
  if digit_ratio > 0.4 then
    score = score + 1
  end

  -- Long random-looking username
  if #username > 15 and entropy(lower) > 3.5 then
    score = score + 1
  end

  -- Ends with many digits (bot-generated pattern)
  local trailing_digits = username:match('(%d+)$')
  if trailing_digits and #trailing_digits >= 4 then
    score = score + 1
  end

  return score > 0, score
end

-- Check if display name has script mixing (Latin + Cyrillic indicators of fake profile)
local function has_suspicious_name_patterns(first_name, last_name)
  local full_name = (first_name or '') .. ' ' .. (last_name or '')
  local score = 0
  local reasons = {}

  -- Emoji in display name
  local emoji_count = count_spam_emoji(full_name)
  if emoji_count > 0 then
    score = score + emoji_count
    table.insert(reasons, string.format('%d spam emoji', emoji_count))
  end

  -- Very short or single-character name
  if first_name and #first_name <= 2 and (not last_name or #last_name == 0) then
    score = score + 1
    table.insert(reasons, 'very short name')
  end

  -- Name is just emoji (no real text)
  local name_no_spaces = full_name:gsub('%s', '')
  -- Count ASCII/Cyrillic letters
  local real_chars = 0
  for i = 1, #name_no_spaces do
    local b = name_no_spaces:byte(i)
    if (b >= 0x41 and b <= 0x7a) or (b >= 0xd0 and b <= 0xd1) then
      real_chars = real_chars + 1
    end
  end
  if #name_no_spaces > 2 and real_chars == 0 then
    score = score + 2
    table.insert(reasons, 'name is only emoji/symbols')
  end

  return score, reasons
end

-- Main callback: analyze Telegram user profile for spam signals
rspamd_config:register_symbol({
  name = 'TELEGRAM_SUSPECT_PROFILE',
  score = 3.0,
  group = 'telegram',
  description = 'Suspicious Telegram user profile (new user only)',
  callback = function(task)
    -- Only check new users
    if task:has_symbol('TELEGRAM_USER_REPUTATION') then
      return false
    end

    local username = get_header(task, 'X-Telegram-Username') or ''
    local first_name = get_header(task, 'X-Telegram-First-Name') or ''
    local last_name = get_header(task, 'X-Telegram-Last-Name') or ''

    local total_score = 0
    local all_reasons = {}

    -- Check random username
    local is_random, random_score = is_random_username(username)
    if is_random then
      total_score = total_score + random_score
      table.insert(all_reasons, 'random username')
    end

    -- Check suspicious name patterns
    local name_score, name_reasons = has_suspicious_name_patterns(first_name, last_name)
    total_score = total_score + name_score
    for _, r in ipairs(name_reasons) do
      table.insert(all_reasons, r)
    end

    if total_score > 0 then
      local weight = math.min(total_score / 3, 1.0)
      lua_util.debugm(N, task, 'suspect profile: score=%s reasons=%s user=%s name=%s %s',
        total_score, table.concat(all_reasons, ', '), username, first_name, last_name)
      return true, weight, table.concat(all_reasons, ', ')
    end

    return false
  end,
})
