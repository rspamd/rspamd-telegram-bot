# Rspamd Telegram Bot

A Telegram bot that monitors group chats for spam using [Rspamd](https://rspamd.com) as the scanning engine. Messages are converted to RFC 2822 MIME format with Telegram metadata in custom headers, scanned via the Rspamd HTTP API, and results are logged to a moderator channel.

## Architecture

```
Telegram → Go Bot → Rspamd (HTTP /checkv2) → verdict
                  → ClickHouse (store messages + results)
                  → Moderator Channel (spam alerts + admin commands)

Bot ↔ Redis ↔ Rspamd (shared: bayes, fuzzy, neural, maps)
    ↔ Shared Maps Volume (admin-managed regexp/URL rules)
```

### Components

| Service | Role |
|---------|------|
| **Bot** | Go binary — receives Telegram updates via long polling, converts messages to MIME, sends to Rspamd |
| **Rspamd** | Spam scanner with custom Telegram-specific Lua rules and multimap filters |
| **Redis** | Shared state between bot and Rspamd (Bayes, fuzzy hashes, neural network, user tracking) |
| **ClickHouse** | Persistent storage for all messages and scan results |

## Features

- Scans all messages in monitored chats through Rspamd
- Custom Lua rules for Telegram-specific spam patterns (crypto spam, new user links, join floods, suspicious names)
- Multimap rules for regexp patterns, URLs, and known spammer user IDs
- Bayes + fuzzy hash training via `/spam` and `/ham` commands
- Admin-managed regexp and URL rules via bot commands in the moderator channel
- Spam reports with score breakdown sent to the moderator channel
- Full message history and scan results stored in ClickHouse

## Quick Start

### Prerequisites

- Docker and Docker Compose
- A Telegram bot token (from [@BotFather](https://t.me/BotFather))

### Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/vstakhov/rspamd-telegram-bot.git
   cd rspamd-telegram-bot
   ```

2. Create configuration files from examples:
   ```bash
   cp .env.example .env
   cp config.example.yml config.yml
   ```

3. Edit `.env` with your secrets:
   ```
   BOT_TOKEN=your_telegram_bot_token_here
   RSPAMD_PASSWORD=your_rspamd_controller_password
   ```

4. To discover chat IDs, start the bot and use `/chatid` in any chat:
   ```bash
   docker compose up -d
   ```
   Add the bot to your groups/channels, send `/chatid`, then update `config.yml`:
   ```yaml
   telegram:
     monitored_chats: [-1001234567890]    # chats to scan
     moderator_channel: -1009876543210    # spam reports + admin commands
   ```

5. Restart after updating config:
   ```bash
   docker compose restart bot
   ```

## Bot Commands

### Anywhere

| Command | Description |
|---------|-------------|
| `/chatid` | Show the current chat's ID (for setting up config) |

### In monitored chats (admin-only)

| Command | Description |
|---------|-------------|
| `/spam` | Reply to a message to train it as spam (Bayes + fuzzy hash) |
| `/ham` | Reply to a message to train it as ham (Bayes + remove fuzzy hash) |

### In moderator channel

| Command | Description |
|---------|-------------|
| `/addregexp <pattern>` | Add a regexp pattern to the spam filter |
| `/delregexp <pattern>` | Remove a regexp pattern |
| `/listregexp` | List all configured regexp patterns |
| `/addurl <domain_or_url>` | Add a URL/domain to the spam URL list |
| `/delurl <domain_or_url>` | Remove a URL/domain |
| `/listurls` | List all configured URL rules |

Regexp patterns use Rspamd format: `/pattern/flags` (e.g., `/crypto.*invest.*profit/i`) or plain text patterns.

Changes take effect automatically — Rspamd watches map files for modifications.

## Shared Maps Volume

The bot and Rspamd share a Docker volume (`maps-data`) for admin-managed map files:

```
maps-data volume
├── spam_patterns.map   ← regexp rules (multimap content filter)
├── spam_urls.map       ← URL/domain blocklist (multimap URL filter)
└── spam_users.map      ← banned user IDs (multimap header filter)
```

- **Bot** mounts the volume at `/maps` and writes to map files when admins issue commands
- **Rspamd** mounts the volume at `/etc/rspamd/maps.d` and reads map files via the multimap module
- Rspamd automatically reloads maps when files change on disk

## Project Structure

```
cmd/bot/main.go              Entry point
internal/
  config/                    YAML + .env config loading
  telegram/                  Bot setup, message handlers, admin commands
  rspamd/                    HTTP client + RFC 2822 message builder
  storage/                   ClickHouse client + schema
  moderator/                 Moderator channel reporter
  maps/                      Map file manager (regexp/URL rules)
rspamd/
  local.d/                   Rspamd module configs (multimap, etc.)
  lua/telegram.lua           Custom Lua rules for Telegram context
  maps.d/                    Seed map files (copied into volume on first run)
  scores.d/                  Score overrides
clickhouse/init.sql          ClickHouse schema
deploy.sh                    Deploy script (rsync + docker compose)
```

## Deployment

```bash
# Full deploy: sync files, rebuild, recreate containers
./deploy.sh myserver

# Other actions
./deploy.sh myserver sync      # only rsync files
./deploy.sh myserver build     # sync + rebuild images
./deploy.sh myserver restart   # sync + restart (no rebuild)
./deploy.sh myserver logs      # tail remote logs
./deploy.sh myserver status    # show container status
```

First deploy requires copying secrets manually:
```bash
scp .env myserver:~/rspamd-telegram-bot/.env
scp config.yml myserver:~/rspamd-telegram-bot/config.yml
```

## Rspamd Integration Details

### Message Format

Telegram messages are wrapped as RFC 2822 MIME messages:
- Standard headers: `From`, `To`, `Subject`, `Date`, `Message-ID`, `In-Reply-To`
- Telegram metadata via `X-Telegram-*` headers (user ID, chat ID, message type, etc.)
- Media attachments as `multipart/mixed` MIME parts

### Custom Rules

**Lua rules** (`rspamd/lua/telegram.lua`):
- `TELEGRAM_NEW_USER_LINK` — new user posting URLs (Redis-tracked first-seen time)
- `TELEGRAM_FORWARD_SPAM` — forwarded message with links
- `TELEGRAM_JOIN_FLOOD` — multiple joins in short period
- `TELEGRAM_SUSPICIOUS_NAME` — display name matches spam patterns
- `TELEGRAM_CRYPTO_SPAM` — crypto/investment text patterns
- `TELEGRAM_SHORT_FIRST_MSG` — short first message from new user with URL

**Multimap rules** (`rspamd/local.d/multimap.conf`):
- `TELEGRAM_SPAM_PATTERN` — matches content against admin-managed regexp patterns
- `TELEGRAM_SPAM_URL` — matches URLs against admin-managed URL blocklist
- `TELEGRAM_SPAM_USER` — matches user ID header against banned user list

## Configuration Reference

### config.yml

```yaml
telegram:
  monitored_chats: [-1001234567890]    # group chat IDs to monitor
  moderator_channel: -1009876543210    # spam reports + admin commands

rspamd:
  url: "http://rspamd:11333"
  password: "${RSPAMD_PASSWORD}"       # references .env
  timeout: 10s

redis:
  addr: "redis:6379"
  db: 0

clickhouse:
  dsn: "clickhouse://clickhouse:9000/telegram_bot"

thresholds:
  log_score: 5.0                       # score to report to moderators
  reject_score: 15.0                   # score for auto-action

maps:
  dir: "/maps"                         # path to shared maps volume
```

### .env

```
BOT_TOKEN=your_telegram_bot_token_here
RSPAMD_PASSWORD=your_rspamd_controller_password
```

## License

Apache License 2.0 - see [LICENSE](LICENSE)
