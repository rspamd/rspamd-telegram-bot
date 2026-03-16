CREATE DATABASE IF NOT EXISTS telegram_bot;

-- Bot-side: Telegram message metadata
-- Rspamd scan details are in the rspamd table (created by rspamd clickhouse plugin)
-- Join key: messages.message_id = toUInt64(trimBoth(rspamd.QueueID, 'tg-'))
CREATE TABLE IF NOT EXISTS telegram_bot.messages (
    message_id    UInt64,
    chat_id       Int64,
    user_id       Int64,
    username      String,
    first_name    String,
    last_name     String,
    text          String,
    message_type  LowCardinality(String),
    has_media     UInt8,
    reply_to_message_id Nullable(UInt64),
    forward_from_id     Nullable(Int64),
    timestamp     DateTime,
    rspamd_score  Float32,
    rspamd_action LowCardinality(String),
    is_spam       UInt8,
    created_at    DateTime DEFAULT now()
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (chat_id, timestamp, message_id);
