# Scripts

Tools for processing Telegram chat exports and preparing training data.

## Prerequisites

```bash
pip3 install beautifulsoup4
```

## Exporting Chat History from Telegram

1. Open Telegram Desktop
2. Go to the target chat/group
3. Click the three-dot menu (top right) → **Export chat history**
4. Settings:
   - Uncheck all media types (photos, videos, etc.) — only text is needed
   - Format: **HTML**
   - Size limit: doesn't matter for text-only
5. Click **Export** — creates a `ChatExport_YYYY-MM-DD/` directory

## parse_export.py

Parses Telegram Desktop HTML exports and extracts messages for neural training and user profiling.

### Quick Start

```bash
# See what's in the export
python3 scripts/parse_export.py /path/to/ChatExport/ --stats

# Extract ham (legitimate messages) for neural training
python3 scripts/parse_export.py /path/to/ChatExport/ -o ham.txt

# Train neural network
./deploy.sh <host> train ham ham.txt
```

### Commands

**Show per-user statistics:**
```bash
python3 scripts/parse_export.py /path/to/ChatExport/ --stats
```
Output: table of users sorted by message count, with first/last seen dates.

**Extract training data:**
```bash
# All messages (for ham training from a legitimate group)
python3 scripts/parse_export.py /path/to/ChatExport/ -o ham.txt

# Sample 4000 random messages from a large export
python3 scripts/parse_export.py /path/to/ChatExport/ -o ham.txt --sample 4000

# Only messages from specific users (for spam training)
python3 scripts/parse_export.py /path/to/ChatExport/ -o spam.txt \
    --users "CryptoBot,SpamUser,AnotherSpammer"

# Skip short messages (default: 10 chars minimum)
python3 scripts/parse_export.py /path/to/ChatExport/ -o ham.txt --min-length 20

# JSON Lines format (preserves original newlines and metadata)
python3 scripts/parse_export.py /path/to/ChatExport/ -o messages.jsonl --format jsonl
```

Output formats:
- `--format text` (default) — one message per line, newlines within messages collapsed to spaces. Compatible with `deploy.sh train`.
- `--format jsonl` — JSON Lines with `from`, `text` (original newlines preserved), `date` fields. For analysis or custom processing.

**Generate Redis profile data:**
```bash
python3 scripts/parse_export.py /path/to/ChatExport/ --profiles profiles.redis
```
Creates Redis protocol commands to seed user profiles (first_seen, last_seen, msg_count, last messages, contacts).

**Combine multiple actions:**
```bash
python3 scripts/parse_export.py /path/to/ChatExport/ \
    --stats \
    -o ham.txt \
    --profiles profiles.redis
```

### Training Workflow

**Step 1: Identify spam vs ham sources**

- Export a legitimate group → ham training data
- Export a spam-heavy group, filter by known spammers → spam training data
- External spam datasets (e.g., `spam.txt` from antispam repos) → spam training data

**Step 2: Extract and train**

```bash
# Ham from your legitimate group
python3 scripts/parse_export.py ~/ChatExport_rspamd/ -o ham.txt
./deploy.sh myserver train ham ham.txt

# Spam from an external dataset
./deploy.sh myserver train spam spam.txt

# Spam from specific users in your group
python3 scripts/parse_export.py ~/ChatExport_rspamd/ -o spam_users.txt \
    --users "CryptoScam,BitcoinProfit"
./deploy.sh myserver train spam spam_users.txt
```

**Step 3: Wait for neural training**

Neural network trains asynchronously after collecting enough samples. Check the `watch_interval` and `max_trains` settings in `rspamd/local.d/neural.conf`. Training status can be checked in rspamd logs:
```bash
./deploy.sh myserver logs | grep -i neural
```

### Loading Redis Profiles

The `--profiles` output generates Redis commands that can be piped directly:

```bash
# Generate
python3 scripts/parse_export.py /path/to/ChatExport/ --profiles profiles.redis

# Upload to server and load
scp profiles.redis myserver:/tmp/
ssh myserver "cd rspamd-telegram-bot && \
    docker compose cp /tmp/profiles.redis redis:/tmp/profiles.redis && \
    docker compose exec redis redis-cli < /tmp/profiles.redis && \
    rm /tmp/profiles.redis"
```

Note: HTML exports don't contain numeric user IDs, so profile keys use display names (`tg_profile:name:john_doe`). Real profiles with proper user IDs are created automatically by the Lua plugin when the bot processes live messages.

### Output Format

**Training data** (`-o`): plain text, one message per line. Compatible with `deploy.sh train`:
```
ok, now it is t.me/rspamd
I've just created this group
has anyone tried the new neural module?
```

**Profiles** (`--profiles`): Redis protocol commands:
```
HSET tg_profile:name:vsevolod_stakhov first_seen 1490972399 last_seen 1710424800 msg_count 4795 first_name "Vsevolod Stakhov" username ""
EXPIRE tg_profile:name:vsevolod_stakhov 7776000
LPUSH tg_profile:name:vsevolod_stakhov:messages "last message text"
...
```
