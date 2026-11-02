#!/usr/bin/env bash
# 每日备份 prod/staging，并清理过期备份。
# 建议由 ubuntu 用户的 cron 执行；ubuntu 已在 docker 组中。
set -uo pipefail

ROOT="${GUANLANAI_ROOT:-/opt/guanlanai}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
DOCKER="${DOCKER_BIN:-/usr/bin/docker}"

if command -v flock >/dev/null 2>&1; then
    exec 9>/tmp/guanlanai-backup.lock
    if ! flock -n 9; then
        echo "[$(date -u +%FT%TZ)] another backup is running"
        exit 0
    fi
fi

exit_code=0
for env in prod staging; do
    dir="$ROOT/$env"
    if [ ! -f "$dir/docker-compose.yml" ]; then
        continue
    fi

    echo "[$(date -u +%FT%TZ)] $env backup start"
    if (cd "$dir" && "$DOCKER" compose exec -T bot guanlanai backup); then
        echo "[$(date -u +%FT%TZ)] $env backup ok"
    else
        echo "[$(date -u +%FT%TZ)] $env backup FAILED"
        exit_code=1
    fi

    if [ -d "$dir/data/backups" ]; then
        find "$dir/data/backups" -maxdepth 1 -type f -name 'guanlanai-*.db' \
            -mtime +"$RETENTION_DAYS" -delete
    fi
done

exit "$exit_code"
