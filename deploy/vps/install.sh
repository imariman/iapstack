#!/bin/sh
# Installs or upgrades the single-VM IAPStack deployment on Ubuntu or Debian.
#
#   curl -fsSL https://raw.githubusercontent.com/imariman/iapstack/main/deploy/vps/install.sh \
#     | sudo CLOUDFLARE_TUNNEL_TOKEN=<token> sh
#
# Settings (environment variables, all optional):
#   IAPSTACK_REF                  Git tag, branch, or commit to deploy (default: the
#                                 previously installed ref, otherwise main)
#   IAPSTACK_REPO                 Repository URL (default: the public IAPStack repository)
#   IAPSTACK_DIR                  Checkout location (default: /opt/iapstack)
#   CLOUDFLARE_TUNNEL_TOKEN       Connector token; without it the stack starts without a tunnel
#   IAPSTACK_BOOTSTRAP_ADMIN_KEY  Bootstrap key to use on first install instead of a generated one
#   IAPSTACK_INSTALL_BACKUP_CRON  "false" skips the daily backup cron entry
#
# Re-running the script upgrades the checkout to IAPSTACK_REF, takes a backup first,
# and keeps the existing .env so secrets and the database stay valid.
set -eu

IAPSTACK_REPO="${IAPSTACK_REPO:-https://github.com/imariman/iapstack.git}"
IAPSTACK_DIR="${IAPSTACK_DIR:-/opt/iapstack}"
# A re-run without IAPSTACK_REF stays on the previously installed ref instead of
# silently moving to main.
ref_file="$IAPSTACK_DIR/.iapstack-ref"
if [ -z "${IAPSTACK_REF:-}" ] && [ -s "$ref_file" ]; then
  IAPSTACK_REF="$(cat "$ref_file")"
fi
IAPSTACK_REF="${IAPSTACK_REF:-main}"
IAPSTACK_INSTALL_BACKUP_CRON="${IAPSTACK_INSTALL_BACKUP_CRON:-true}"

log() { printf '[iapstack-install] %s\n' "$*"; }
fail() { printf '[iapstack-install] ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root, for example with sudo"
command -v apt-get >/dev/null 2>&1 || fail "only Ubuntu and Debian hosts are supported"

# Small hosts (1 GB) need swap to build the Go image and run PostgreSQL comfortably.
add_swap_if_needed() {
  mem_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)"
  swap_kb="$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)"
  if [ "$mem_kb" -lt 2000000 ] && [ "$swap_kb" -eq 0 ] && [ ! -e /swapfile ]; then
    log "less than 2 GB RAM and no swap: creating a 2 GB swap file"
    if fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile; then
      echo '/swapfile none swap sw 0 0' >> /etc/fstab
    else
      rm -f /swapfile
      log "could not enable swap; continuing without it"
    fi
  fi
}

install_packages() {
  log "installing base packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -q
  apt-get install -y -q ca-certificates curl git openssl cron >/dev/null

  if ! command -v docker >/dev/null 2>&1; then
    log "installing Docker Engine from get.docker.com"
    curl -fsSL https://get.docker.com | sh
  fi
  docker compose version >/dev/null 2>&1 || apt-get install -y -q docker-compose-plugin >/dev/null
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl enable --now docker >/dev/null 2>&1 || true
    systemctl enable --now cron >/dev/null 2>&1 || true
  fi
  docker info >/dev/null 2>&1 || fail "Docker is installed but the daemon is not reachable"
}

checkout_source() {
  if [ ! -d "$IAPSTACK_DIR/.git" ]; then
    log "cloning $IAPSTACK_REPO into $IAPSTACK_DIR"
    mkdir -p "$IAPSTACK_DIR"
    git -C "$IAPSTACK_DIR" init -q
    git -C "$IAPSTACK_DIR" remote add origin "$IAPSTACK_REPO"
  fi
  log "checking out $IAPSTACK_REF"
  git -C "$IAPSTACK_DIR" fetch -q --depth 1 origin "$IAPSTACK_REF"
  git -C "$IAPSTACK_DIR" checkout -q --detach FETCH_HEAD
  printf '%s\n' "$IAPSTACK_REF" > "$ref_file"
  log "deploying commit $(git -C "$IAPSTACK_DIR" rev-parse --short HEAD)"
}

# set_env KEY VALUE replaces or appends KEY=VALUE in .env without shell or sed escaping.
set_env() {
  tmp="$(mktemp ./.env.XXXXXX)"
  KEY="$1" VALUE="$2" awk '
    BEGIN { key = ENVIRON["KEY"]; value = ENVIRON["VALUE"]; done = 0 }
    index($0, key "=") == 1 { print key "=" value; done = 1; next }
    { print }
    END { if (!done) print key "=" value }
  ' .env > "$tmp"
  chmod 600 "$tmp"
  mv "$tmp" .env
}

env_value() { sed -n "s/^$1=//p" .env | tail -n 1; }

configure_env() {
  if [ ! -e .env ]; then
    ./init-env.sh
    if [ -n "${IAPSTACK_BOOTSTRAP_ADMIN_KEY:-}" ]; then
      set_env IAPSTACK_BOOTSTRAP_ADMIN_KEY "$IAPSTACK_BOOTSTRAP_ADMIN_KEY"
    fi
  else
    log "keeping existing .env"
  fi
  if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ]; then
    set_env CLOUDFLARE_TUNNEL_TOKEN "$CLOUDFLARE_TUNNEL_TOKEN"
  fi
}

backup_before_upgrade() {
  if docker compose ps --status running --services 2>/dev/null | grep -qx postgres; then
    log "taking a backup before upgrading"
    ./backup.sh || fail "pre-upgrade backup failed; nothing was changed"
  fi
}

start_stack() {
  if [ -n "$(env_value CLOUDFLARE_TUNNEL_TOKEN)" ]; then
    log "building and starting PostgreSQL, IAPStack, and the Cloudflare Tunnel"
    docker compose up --build -d --wait
  else
    log "CLOUDFLARE_TUNNEL_TOKEN is empty: starting without the tunnel"
    docker compose up --build -d --wait postgres migrate iapstack
  fi
  docker image prune -f >/dev/null 2>&1 || true
}

install_backup_cron() {
  [ "$IAPSTACK_INSTALL_BACKUP_CRON" = "true" ] || return 0
  cat > /etc/cron.d/iapstack-backup <<EOF
# Daily IAPStack PostgreSQL backup, installed by deploy/vps/install.sh.
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
17 3 * * * root $IAPSTACK_DIR/deploy/vps/backup.sh >> $IAPSTACK_DIR/deploy/vps/backup.log 2>&1
EOF
  chmod 644 /etc/cron.d/iapstack-backup
  log "installed daily backup cron entry (/etc/cron.d/iapstack-backup)"
}

add_swap_if_needed
install_packages
checkout_source
cd "$IAPSTACK_DIR/deploy/vps"
backup_before_upgrade
configure_env
start_stack
install_backup_cron

port="$(env_value IAPSTACK_HTTP_PORT)"
curl -fsS "http://127.0.0.1:${port:-8080}/readyz" >/dev/null || fail "IAPStack did not become ready"

log "IAPStack is running."
if [ -z "$(env_value CLOUDFLARE_TUNNEL_TOKEN)" ]; then
  log "Next: add CLOUDFLARE_TUNNEL_TOKEN to $IAPSTACK_DIR/deploy/vps/.env and re-run this script."
fi
if [ -n "$(env_value IAPSTACK_BOOTSTRAP_ADMIN_KEY)" ]; then
  log "Bootstrap administrator key: sudo grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= $IAPSTACK_DIR/deploy/vps/.env"
  log "Create a durable administrator in /dashboard/, then empty that value and re-run this script."
fi
log "Secrets live in $IAPSTACK_DIR/deploy/vps/.env; copy them to your password manager."
