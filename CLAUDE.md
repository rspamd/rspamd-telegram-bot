# Rspamd Telegram Bot

## Project Overview

A Telegram bot that monitors group chats for spam using Rspamd as the scanning engine. Messages are wrapped as RFC 2822 MIME messages with Telegram metadata in custom headers, sent to Rspamd via HTTP API, and results are logged to a moderator channel.

## Tech Stack

- **Bot**: Go (statically compiled)
- **Spam Engine**: Rspamd with custom Telegram-specific rules
- **State/Cache**: Redis (shared between bot and Rspamd)
- **Storage**: ClickHouse (message history + scan results)
- **Deployment**: Docker Compose

## Architecture

```
Telegram → Go Bot → Rspamd (HTTP /checkv2) → verdict
                  → ClickHouse (store all messages + results)
                  → Moderator Channel (if spam detected)
Bot ↔ Redis ↔ Rspamd (shared state: bayes, fuzzy, neural, maps)
```

## Project Structure

```
cmd/bot/main.go              - Entry point
internal/config/             - YAML + .env config loading
internal/telegram/           - Bot setup, message handlers, commands
internal/rspamd/             - HTTP client + RFC 2822 message builder
internal/storage/            - ClickHouse client + schema
internal/moderator/          - Moderator channel reporter
internal/maps/               - Map file manager (admin-managed rules)
rspamd/                      - Rspamd Docker config, rules, maps
rspamd/local.d/              - Rspamd module configs
rspamd/lua.local.d/          - Custom Lua plugins (auto-loaded by rspamd)
clickhouse/                  - ClickHouse init SQL
```

## Conventions

- Go: standard project layout, `internal/` for private packages
- Config: `config.yml` for settings, `.env` for secrets (bot token, etc.)
- Error handling: wrap errors with context using `fmt.Errorf("...: %w", err)`
- Logging: structured logging via `log/slog`
- No CGO: pure Go for static compilation
- Docker: multi-stage builds, minimal final images

## Rspamd Integration

- Messages sent as RFC 2822 with MIME structure
- Telegram metadata passed via `X-Telegram-*` headers
- Media attachments as multipart/mixed MIME parts
- Thread tracking via In-Reply-To header simulation
- Bot commands `/spam` and `/ham` train Bayes + fuzzy

## Key Decisions

- Long polling (no webhook, no TLS needed in Docker)
- Single shared Redis instance for bot + Rspamd
- Moderator notifications via Telegram channel (dashboard later)
- All Rspamd rules custom-tailored for Telegram context
