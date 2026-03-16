#!/usr/bin/env bash
set -euo pipefail

# Deploy rspamd-telegram-bot to a remote host.
#
# Usage:
#   ./deploy.sh <host>              # full deploy (sync + build + recreate)
#   ./deploy.sh <host> sync         # only rsync files
#   ./deploy.sh <host> build        # sync + rebuild images
#   ./deploy.sh <host> restart      # sync + restart without rebuild
#   ./deploy.sh <host> logs         # tail remote logs
#   ./deploy.sh <host> status       # show remote container status
#   ./deploy.sh <host> train spam <file>  # train neural on spam
#   ./deploy.sh <host> train ham <file>   # train neural on ham
#   ./deploy.sh <host> maps         # deploy seed maps into rspamd volume
#   ./deploy.sh <host> backup [dir]  # backup Redis + ClickHouse + maps to local dir
#   ./deploy.sh <host> restore [dir] # restore backup to host
#
# <host> is an SSH destination (e.g. user@server, or an ~/.ssh/config alias).
#
# Environment variables:
#   REMOTE_DIR   - deployment path on remote host (default: ~/rspamd-telegram-bot)
#   COMPOSE_CMD  - docker compose command (default: auto-detect)

HOST="${1:?Usage: $0 <host> [sync|build|restart|logs|status|train]}"
ACTION="${2:-deploy}"
REMOTE_DIR="${REMOTE_DIR:-~/rspamd-telegram-bot}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Files/dirs to sync (excludes secrets and build artifacts)
RSYNC_INCLUDES=(
    cmd/
    internal/
    rspamd/
    clickhouse/
    go.mod
    go.sum
    Dockerfile
    docker-compose.yml
    config.example.yml
    README.md
    CLAUDE.md
    deploy.sh
)

ssh_cmd() {
    ssh -o BatchMode=yes "$HOST" "$@"
}

detect_compose() {
    # Cache the result per host
    if [ -z "${COMPOSE_CMD:-}" ]; then
        if ssh_cmd "docker compose version" &>/dev/null; then
            COMPOSE_CMD="docker compose"
        elif ssh_cmd "command -v docker-compose" &>/dev/null; then
            COMPOSE_CMD="docker-compose"
        else
            echo "Error: neither 'docker compose' nor 'docker-compose' found on $HOST" >&2
            exit 1
        fi
    fi
}

do_sync() {
    echo "==> Syncing project to $HOST:$REMOTE_DIR"

    # Build rsync include/exclude args
    local rsync_args=(
        -avz --delete
        --exclude='.git/'
        --exclude='.env'
        --exclude='config.yml'
        --exclude='.DS_Store'
        --exclude='*.swp'
        --exclude='vendor/'
        --exclude='.idea/'
        --exclude='.vscode/'
        --exclude='.claude/'
    )

    # Ensure remote directory exists
    ssh_cmd "mkdir -p $REMOTE_DIR"

    rsync "${rsync_args[@]}" \
        "${SCRIPT_DIR}/" \
        "$HOST:$REMOTE_DIR/"

    echo "    Sync complete."
}

do_maps() {
    detect_compose
    local seed_dir="${SCRIPT_DIR}/rspamd/seed-maps"

    # Collect .map and .cf files
    local files=()
    for f in "$seed_dir"/*.map "$seed_dir"/*.cf; do
        [ -e "$f" ] && files+=("$f")
    done

    if [ ${#files[@]} -eq 0 ]; then
        echo "    No seed maps found in $seed_dir"
        return
    fi

    echo "==> Deploying seed maps to rspamd volume on $HOST"

    for f in "${files[@]}"; do
        local name
        name=$(basename "$f")
        scp -q "$f" "$HOST:/tmp/$name"
        ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp /tmp/$name rspamd:/etc/rspamd/maps.d/$name && rm -f /tmp/$name"
        echo "    $name"
    done

    echo "    Maps deployed. Rspamd will reload automatically."
}

do_build() {
    detect_compose
    echo "==> Pruning Docker on $HOST"
    ssh_cmd "docker system prune -af --filter 'until=1h'" 2>/dev/null || true
    ssh_cmd "docker builder prune -af" 2>/dev/null || true
    echo "==> Building images on $HOST"
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD build --pull"
    echo "    Build complete."
}

do_recreate() {
    detect_compose
    echo "==> Recreating containers on $HOST"
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD up -d --force-recreate --remove-orphans"
    echo "    Containers recreated."
}

do_restart() {
    detect_compose
    echo "==> Restarting containers on $HOST"
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD up -d --remove-orphans"
    echo "    Containers restarted."
}

do_logs() {
    detect_compose
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD logs -f --tail=100"
}

do_status() {
    detect_compose
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD ps"
}

check_secrets() {
    local missing=()
    if ! ssh_cmd "test -f $REMOTE_DIR/.env"; then
        missing+=(".env")
    fi
    if ! ssh_cmd "test -f $REMOTE_DIR/config.yml"; then
        missing+=("config.yml")
    fi

    if [ ${#missing[@]} -gt 0 ]; then
        echo ""
        echo "WARNING: Missing files on remote host:"
        for f in "${missing[@]}"; do
            echo "  - $REMOTE_DIR/$f"
        done
        echo ""
        echo "Copy them manually (they contain secrets and are not synced):"
        echo "  scp .env $HOST:$REMOTE_DIR/.env"
        echo "  scp config.yml $HOST:$REMOTE_DIR/config.yml"
        echo ""
        return 1
    fi
    return 0
}

do_train() {
    local class="${1:-}"
    local file="${2:-}"

    if [ -z "$class" ] || [ -z "$file" ]; then
        echo "Usage: $0 <host> train <spam|ham> <file>" >&2
        echo "File format: one message per line (plain text)" >&2
        exit 1
    fi

    if [ ! -f "$file" ]; then
        echo "Error: file not found: $file" >&2
        exit 1
    fi

    case "$class" in
        spam|ham) ;;
        *)  echo "Error: class must be 'spam' or 'ham', got: $class" >&2; exit 1 ;;
    esac

    detect_compose

    local total
    total=$(grep -cv '^[[:space:]]*$' "$file" || true)
    echo "==> Training neural ($class) on $HOST ($total messages)"

    # Copy training file into the rspamd container
    scp -q "$file" "$HOST:/tmp/rspamd_train.txt"
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp /tmp/rspamd_train.txt rspamd:/tmp/rspamd_train.txt && rm -f /tmp/rspamd_train.txt"

    # Train neural network using rspamc neural_learn:<class> command.
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD exec -T rspamd sh -c '
        count=0
        ok=0
        fail=0
        while IFS= read -r line || [ -n \"\$line\" ]; do
            # skip empty lines and comments
            case \"\$line\" in
                \"\"|\#*) continue ;;
            esac

            # wrap as MIME message
            msg=\"From: train@telegram.local\r\nTo: train@telegram.local\r\nSubject: training\r\nContent-Type: text/html; charset=utf-8\r\nMIME-Version: 1.0\r\n\r\n\${line}\r\n\"

            if printf \"%b\" \"\$msg\" | rspamc -h localhost:11334 --header \"Settings-ID: telegram\" \"neural_learn:$class\" 2>/dev/null; then
                ok=\$((ok + 1))
            else
                fail=\$((fail + 1))
            fi

            count=\$((count + 1))
            if [ \$((count % 50)) -eq 0 ]; then
                printf \"    %d / %d\\n\" \"\$count\" \"$total\"
            fi
        done < /tmp/rspamd_train.txt
        printf \"    Done: %d ok, %d failed out of %d\\n\" \"\$ok\" \"\$fail\" \"\$count\"
        rm -f /tmp/rspamd_train.txt
    '"

    echo "==> Training complete. Neural will train automatically once enough samples collected."
    echo "    (min ${total} spam + ham samples needed, see neural.conf max_trains)"
}

do_backup() {
    detect_compose
    local backup_dir="${1:-${SCRIPT_DIR}/backups/$(date +%Y%m%d_%H%M%S)_${HOST}}"
    mkdir -p "$backup_dir"

    echo "==> Backing up data from $HOST to $backup_dir"

    # Redis dump
    echo "    Redis..."
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD exec -T redis redis-cli BGSAVE && sleep 2"
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp redis:/data/dump.rdb /tmp/redis_dump.rdb"
    scp -q "$HOST:/tmp/redis_dump.rdb" "$backup_dir/redis.rdb"
    ssh_cmd "rm -f /tmp/redis_dump.rdb"

    # ClickHouse backup (SQL dump of our tables)
    echo "    ClickHouse..."
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD exec -T clickhouse clickhouse-client \
        --query 'SELECT * FROM telegram_bot.messages FORMAT Native' > /tmp/ch_messages.native" 2>/dev/null
    scp -q "$HOST:/tmp/ch_messages.native" "$backup_dir/ch_messages.native" 2>/dev/null || true
    ssh_cmd "rm -f /tmp/ch_messages.native"

    # Maps from volume
    echo "    Maps..."
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp rspamd:/etc/rspamd/maps.d /tmp/maps_backup"
    scp -rq "$HOST:/tmp/maps_backup/" "$backup_dir/maps/"
    ssh_cmd "rm -rf /tmp/maps_backup"

    # Config files (secrets)
    echo "    Configs..."
    scp -q "$HOST:$REMOTE_DIR/.env" "$backup_dir/dot_env" 2>/dev/null || true
    scp -q "$HOST:$REMOTE_DIR/config.yml" "$backup_dir/config.yml" 2>/dev/null || true

    echo "==> Backup complete: $backup_dir"
    ls -lh "$backup_dir/"
}

do_restore() {
    local backup_dir="${1:-}"
    if [ -z "$backup_dir" ] || [ ! -d "$backup_dir" ]; then
        echo "Usage: $0 <host> restore <backup_dir>" >&2
        echo "Available backups:" >&2
        ls -d "${SCRIPT_DIR}/backups/"* 2>/dev/null || echo "  (none)" >&2
        exit 1
    fi

    detect_compose
    echo "==> Restoring backup from $backup_dir to $HOST"

    # Ensure stack is running
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD up -d"
    sleep 5

    # Restore configs
    if [ -f "$backup_dir/dot_env" ]; then
        echo "    Configs..."
        scp -q "$backup_dir/dot_env" "$HOST:$REMOTE_DIR/.env"
    fi
    if [ -f "$backup_dir/config.yml" ]; then
        scp -q "$backup_dir/config.yml" "$HOST:$REMOTE_DIR/config.yml"
    fi

    # Restore Redis
    if [ -f "$backup_dir/redis.rdb" ]; then
        echo "    Redis..."
        ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD stop redis"
        scp -q "$backup_dir/redis.rdb" "$HOST:/tmp/redis_dump.rdb"
        ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp /tmp/redis_dump.rdb redis:/data/dump.rdb && rm -f /tmp/redis_dump.rdb"
        ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD start redis"
        sleep 2
    fi

    # Restore ClickHouse
    if [ -f "$backup_dir/ch_messages.native" ]; then
        echo "    ClickHouse..."
        scp -q "$backup_dir/ch_messages.native" "$HOST:/tmp/ch_messages.native"
        ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD exec -T clickhouse clickhouse-client \
            --query 'INSERT INTO telegram_bot.messages FORMAT Native' < /tmp/ch_messages.native" 2>/dev/null || true
        ssh_cmd "rm -f /tmp/ch_messages.native"
    fi

    # Restore maps
    if [ -d "$backup_dir/maps" ]; then
        echo "    Maps..."
        for f in "$backup_dir/maps/"*; do
            [ -e "$f" ] || continue
            local name
            name=$(basename "$f")
            scp -q "$f" "$HOST:/tmp/$name"
            ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD cp /tmp/$name rspamd:/etc/rspamd/maps.d/$name && rm -f /tmp/$name"
        done
    fi

    # Restart everything
    echo "    Restarting..."
    ssh_cmd "cd $REMOTE_DIR && $COMPOSE_CMD restart"

    echo "==> Restore complete."
}

case "$ACTION" in
    sync)
        do_sync
        ;;
    build)
        do_sync
        do_build
        ;;
    restart)
        do_sync
        check_secrets || exit 1
        do_restart
        ;;
    deploy)
        do_sync
        if ! check_secrets; then
            echo "Fix the above before deploying."
            exit 1
        fi
        do_build
        do_recreate
        do_maps
        echo ""
        echo "==> Deploy complete. Use '$0 $HOST logs' to follow output."
        ;;
    logs)
        do_logs
        ;;
    status)
        do_status
        ;;
    maps)
        do_maps
        ;;
    backup)
        do_backup "${3:-}"
        ;;
    restore)
        do_restore "${3:-}"
        ;;
    train)
        do_train "${3:-}" "${4:-}"
        ;;
    *)
        echo "Unknown action: $ACTION" >&2
        echo "Valid actions: sync, build, restart, deploy, logs, status, maps, backup, restore, train" >&2
        exit 1
        ;;
esac
