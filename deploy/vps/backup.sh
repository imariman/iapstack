#!/bin/sh
# Takes a compressed logical PostgreSQL backup of the single-VM stack, keeps recent
# copies on the host, and optionally uploads them with rclone.
#
# Schedule it from the host's crontab, for example daily at 03:17 UTC:
#   17 3 * * * /opt/iapstack/deploy/vps/backup.sh >> /var/log/iapstack-backup.log 2>&1
set -eu

cd "$(dirname "$0")"

if [ -f .env ]; then
  # Read only the backup settings; secrets are not needed by this script.
  IAPSTACK_BACKUP_RCLONE_TARGET="${IAPSTACK_BACKUP_RCLONE_TARGET:-$(sed -n 's/^IAPSTACK_BACKUP_RCLONE_TARGET=//p' .env | tail -n 1)}"
  IAPSTACK_BACKUP_KEEP_DAYS="${IAPSTACK_BACKUP_KEEP_DAYS:-$(sed -n 's/^IAPSTACK_BACKUP_KEEP_DAYS=//p' .env | tail -n 1)}"
fi

backup_dir="${IAPSTACK_BACKUP_DIR:-./backups}"
keep_days="${IAPSTACK_BACKUP_KEEP_DAYS:-14}"
rclone_target="${IAPSTACK_BACKUP_RCLONE_TARGET:-}"

umask 077
mkdir -p "$backup_dir"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
file="$backup_dir/iapstack-$timestamp.dump"
# Never leave a partial dump behind: it holds database data and retention ignores it.
trap 'rm -f "$file.partial"' EXIT

docker compose exec -T postgres \
  pg_dump -U iapstack -d iapstack --format=custom --compress=6 > "$file.partial"

# A custom-format archive that pg_restore cannot list is not a usable backup.
docker compose exec -T postgres pg_restore --list < "$file.partial" > /dev/null
mv "$file.partial" "$file"
echo "$(date -u +%FT%TZ) created $file ($(wc -c < "$file" | tr -d ' ') bytes)"

find "$backup_dir" -name 'iapstack-*.dump' -type f -mtime +"$keep_days" -delete

if [ -n "$rclone_target" ]; then
  rclone copy "$file" "$rclone_target"
  rclone delete --min-age "${keep_days}d" --include 'iapstack-*.dump' "$rclone_target"
  echo "$(date -u +%FT%TZ) uploaded to $rclone_target"
fi
