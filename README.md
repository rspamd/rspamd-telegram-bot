# Rspamd Telegram Bot

A Telegram bot that monitors group chats for spam using [Rspamd](https://rspamd.com) as the scanning engine. Combines neural network classification, LLM-based analysis (GPT), user profiling, profile photo analysis, and quiz verification for new users.

## Architecture

```
Telegram → Go Bot → Rspamd (HTTP /checkv2) → verdict
                  → ClickHouse (store messages + scan results)
                  → Moderator Channel (spam reports + admin commands)
                  → OpenRouter (GPT spam check + userpic analysis + quiz)

Bot ↔ Redis ↔ Rspamd (shared: neural, fuzzy, user profiles, channel context)
```

### Components

| Service | Role |
|---------|------|
| **Bot** | Go binary — long polling, MIME message builder, user profiling, quiz system |
| **Rspamd** | Spam scanner — neural network, SA regexp rules, multimap, GPT module, custom Lua plugins |
| **Redis** | Shared state — user profiles, neural training data, channel context, quiz sessions |
| **ClickHouse** | Persistent storage — message history, Rspamd scan results (via Rspamd plugin) |
| **OpenRouter** | LLM API — GPT spam classification, userpic vision analysis, quiz Q&A |

## Features

### Spam Detection
- **Neural network** with FastText embeddings (per-language models)
- **GPT/LLM classification** for first-time users with channel context
- **SA regexp rules** — 300+ rules extracted from real antispam bot logs
- **Profile analysis** — detect Unicode font abuse, random usernames, spam emoji
- **Userpic analysis** — vision LLM detects stock photos, fake profiles
- **Multimap** — URL blocklist, user ID blocklist, regexp patterns
- **Chartable** — mixed charset / homoglyph detection
- **Fuzzy hashing** — catch variations of known spam

### User Profiling
- Track first seen, message count, contacts, channels per user
- Admin endorsement (reply from admin = reputation boost)
- Reputation system: new → known → active → established
- GPT verdict and userpic analysis cached in profile

### Quiz Verification
- Per-channel LLM-generated questions for suspicious or all new users
- Deep link flow: channel notification → private chat → answer → LLM evaluation
- Lazy question generation (only when user clicks link)
- 1 minute timeout → auto-fail + ban
- Mode: `suspicious` (triggered by spam signals) or `all` (every join)

### Admin Tools
- Forward message to moderator channel → auto-scan with full report
- `/trainspam`, `/trainham` — train neural on forwarded messages
- `/checkprofile` — run profile analysis on any user
- `/userinfo` — full profile dump (reputation, quiz result, userpic, GPT verdict)
- `/addregexp`, `/addurl` — manage spam rules live

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Telegram bot token (from [@BotFather](https://t.me/BotFather))
- OpenRouter API key (for GPT/vision features, optional)

### Setup

1. Clone and configure:
   ```bash
   git clone https://github.com/vstakhov/rspamd-telegram-bot.git
   cd rspamd-telegram-bot
   cp .env.example .env
   cp config.example.yml config.yml
   ```

2. Edit `.env`:
   ```
   BOT_TOKEN=your_telegram_bot_token
   RSPAMD_PASSWORD=your_rspamd_password
   OPENROUTER_API_KEY=sk-or-...
   OPENROUTER_MODEL=google/gemini-2.0-flash-001
   OPENROUTER_VISION_MODEL=google/gemini-2.0-flash-001
   ```

3. Discover chat IDs — start the bot, add to groups, use `/chatid`:
   ```bash
   docker compose up -d
   ```

4. Update `config.yml` with chat IDs and restart:
   ```yaml
   telegram:
     monitored_chats: [-1001234567890]
     moderator_channel: -1009876543210
   ```
   ```bash
   docker compose restart bot
   ```

## Bot Commands

### Anywhere

| Command | Description |
|---------|-------------|
| `/chatid` | Show current chat ID |
| `/help` | List all commands + rspamd version |

### Monitored chats (admin-only)

| Command | Description |
|---------|-------------|
| `/spam` | Reply to train as spam (neural + fuzzy) |
| `/ham` | Reply to train as ham |

### Moderator channel

| Command | Description |
|---------|-------------|
| Forward a message | Auto-scan with full symbol report + userpic analysis |
| `/trainspam` | Reply to forwarded → train neural as spam + add fuzzy hash |
| `/trainham` | Reply to forwarded → train neural as ham |
| `/checkprofile <@user\|ID>` | Run profile analysis through rspamd |
| `/userinfo <@user\|ID>` | Show full user profile |
| `/delprofile <@user\|ID>` | Delete user profile (for testing) |
| `/channels` | List tracked channels |
| `/users <channel>` | Top users in channel |
| `/context [channel]` | Show/list channel context for GPT |

### Rule management

| Command | Description |
|---------|-------------|
| `/addregexp <pattern>` | Add regexp spam rule |
| `/delregexp <pattern>` | Remove regexp rule |
| `/listregexp` | List regexp rules |
| `/addurl <url>` | Add spam URL |
| `/delurl <url>` | Remove URL |
| `/listurls` | List URL rules |

### Quiz system

| Command | Description |
|---------|-------------|
| `/quiz prompt <channel> <text>` | Set LLM prompt for generating questions |
| `/quiz message <channel> <text>` | Set channel notification ({link}, {user} placeholders) |
| `/quiz mode <channel> <suspicious\|all>` | When to quiz: spam signals only or every join |
| `/quiz show <channel>` | Show quiz config |
| `/quiz test <channel>` | Test quiz flow in moderator chat |

All channel arguments accept names (e.g., `freebsd_ru`) or numeric IDs.

## Deployment

```bash
# Full deploy: sync + build + recreate + deploy maps
./deploy.sh myserver

# Other actions
./deploy.sh myserver sync         # rsync files only
./deploy.sh myserver build        # sync + rebuild images
./deploy.sh myserver restart      # sync + restart (no rebuild)
./deploy.sh myserver logs         # tail remote logs
./deploy.sh myserver status       # container status
./deploy.sh myserver maps         # deploy seed maps to rspamd volume
./deploy.sh myserver train spam <file>   # train neural on spam corpus
./deploy.sh myserver train ham <file>    # train neural on ham corpus
./deploy.sh myserver backup [dir]        # backup Redis + ClickHouse + maps
./deploy.sh myserver restore <dir>       # restore backup to host
```

First deploy — copy secrets manually:
```bash
scp .env myserver:~/rspamd-telegram-bot/.env
scp config.yml myserver:~/rspamd-telegram-bot/config.yml
```

## Project Structure

```
cmd/bot/main.go              Entry point
internal/
  config/                    YAML + .env config loading
  telegram/                  Bot handlers, commands, quiz, context, profiling
  rspamd/                    HTTP client + RFC 2822 MIME message builder
  storage/                   ClickHouse client
  moderator/                 Spam report formatter
  maps/                      Map file manager (admin-managed rules)
  quiz/                      Quiz session manager + LLM client
  userpic/                   Profile photo vision analysis
rspamd/
  local.d/                   Rspamd module configs
  lua.local.d/               Custom Lua plugins (auto-loaded)
    telegram.lua             Detection rules (new user link, join flood, etc.)
    telegram_profile.lua     User profiling + reputation (Redis scripts)
    telegram_suspect.lua     Suspicious profile detector
  modules.local.d/           Module registration configs
  maps.d/                    Seed map files (SA rules, URL blocklist, profile rules)
  seed-maps/                 Generated maps from scripts (gitignored)
  fasttext/                  FastText models (gitignored, deploy only)
scripts/
  parse_export.py            Parse Telegram HTML exports for training data
  parse_bot_logs.py          Extract rules + spam corpus from antispam bot logs
clickhouse/init.sql          ClickHouse schema
deploy.sh                    Deploy, train, backup/restore script
```

## Rspamd Symbols

### Telegram rules (Lua)
| Symbol | Score | Description |
|--------|-------|-------------|
| `TELEGRAM_NEW_USER_LINK` | 6.0 | New user posting URLs |
| `TELEGRAM_FORWARD_SPAM` | 4.0 | Forwarded message with links |
| `TELEGRAM_JOIN_FLOOD` | 3.0 | Multiple joins in short period |
| `TELEGRAM_SUSPICIOUS_NAME` | 4.0 | Display name matches spam patterns |
| `TELEGRAM_SHORT_FIRST_MSG` | 5.0 | Short first message with URL from new user |
| `TELEGRAM_USER_REPUTATION` | -1.0 | Reputation bonus (multiplied by weight) |
| `TELEGRAM_SUSPECT_PROFILE` | 4.0 | Random username, spam emoji in name |
| `TELEGRAM_SUSPECT_USERPIC` | 5.0 | Suspicious profile photo (vision LLM) |
| `TELEGRAM_QUIZ_FAILED` | 8.0 | User failed quiz verification |

### Profile rules (SA regexp, multimap)
| Symbol | Score | Description |
|--------|-------|-------------|
| `TG_NAME_ENCLOSED_ALPHA` | 6.0 | Name uses enclosed Unicode letters (🄺🄰🅁🄸🄽🄰) |
| `TG_NAME_MATH_FONT` | 6.0 | Name uses mathematical Unicode font (𝕬𝖓𝖓𝖆) |
| `TG_SPAMMER_PROFILE_STRONG` | 5.0 | Both first+last name use fancy Unicode |
| `TG_USERNAME_RANDOM` | 3.0 | Randomly generated username |
| `TG_PREMIUM_SPAMMER` | 3.0 | Premium account with suspicious profile |

### Neural / GPT
| Symbol | Score | Description |
|--------|-------|-------------|
| `NEURAL_SPAM` | 10.0 | Neural network detected spam |
| `NEURAL_HAM` | -5.0 | Neural network detected ham |
| `GPT_SPAM` | 12.0 | GPT classified as spam |
| `GPT_HAM` | -6.0 | GPT classified as ham |

## Configuration

### .env
```
BOT_TOKEN=your_telegram_bot_token
RSPAMD_PASSWORD=your_rspamd_password
OPENROUTER_API_KEY=sk-or-...              # for GPT, vision, quiz
OPENROUTER_MODEL=google/gemini-2.0-flash-001
OPENROUTER_VISION_MODEL=google/gemini-2.0-flash-001
```

### config.yml
```yaml
telegram:
  monitored_chats: [-1001234567890]
  moderator_channel: -1009876543210

rspamd:
  url: "http://rspamd:11333"
  password: "${RSPAMD_PASSWORD}"
  timeout: 10s

redis:
  addr: "redis:6379"
  db: 0

clickhouse:
  dsn: "clickhouse://clickhouse:9000/telegram_bot"

thresholds:
  log_score: 5.0
  reject_score: 15.0

maps:
  dir: "/maps"
```

## License

Apache License 2.0 — see [LICENSE](LICENSE)
