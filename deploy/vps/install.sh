#!/bin/sh
# Installs or upgrades the single-VM IAPStack deployment on Ubuntu or Debian.
#
#   curl -fsSLO https://raw.githubusercontent.com/imariman/iapstack/main/deploy/vps/install.sh
#   read -rs CLOUDFLARE_TUNNEL_TOKEN && export CLOUDFLARE_TUNNEL_TOKEN
#   sudo --preserve-env=CLOUDFLARE_TUNNEL_TOKEN IAPSTACK_REF=main sh install.sh
#
# Settings (environment variables, all optional):
#   IAPSTACK_REF                  Git tag, branch, or commit to deploy. Without it, an
#                                 existing checkout stays on its current commit and a
#                                 new one uses main.
#   IAPSTACK_REPO                 Repository URL (default: the public IAPStack repository)
#   IAPSTACK_DIR                  Checkout location (default: /opt/iapstack)
#   CLOUDFLARE_TUNNEL_TOKEN       Connector token; without it the stack starts without a tunnel
#   IAPSTACK_BOOTSTRAP_ADMIN_KEY  Bootstrap key to use on first install instead of a generated one
#   IAPSTACK_INSTALL_BACKUP_CRON  "false" skips the daily backup cron entry
#
# Re-running the script is the upgrade path: it backs up the database before changing
# commits, keeps the existing .env so secrets stay valid, and restarts the stack.
set -eu

IAPSTACK_REPO="${IAPSTACK_REPO:-https://github.com/imariman/iapstack.git}"
IAPSTACK_DIR="${IAPSTACK_DIR:-/opt/iapstack}"
IAPSTACK_REF="${IAPSTACK_REF:-}"
IAPSTACK_INSTALL_BACKUP_CRON="${IAPSTACK_INSTALL_BACKUP_CRON:-true}"

log() { printf '[iapstack-install] %s\n' "$*"; }
fail() { printf '[iapstack-install] ERROR: %s\n' "$*" >&2; exit 1; }

# Every failure, including set -e aborts, ends with one ERROR line that log readers
# and deploy/gcp/deploy.sh can detect.
on_exit() {
  status=$?
  if [ "$status" -ne 0 ]; then
    printf '[iapstack-install] ERROR: installation failed (exit %s)\n' "$status" >&2
  fi
}
trap on_exit EXIT

[ "$(id -u)" -eq 0 ] || fail "run as root, for example with sudo"
command -v apt-get >/dev/null 2>&1 || fail "only Ubuntu and Debian hosts are supported"

# Fingerprint of the running installer, used to switch to the checked-out version.
self_sum="$( { [ -f "$0" ] && cksum < "$0"; } 2>/dev/null || echo none)"

# Hosts below ~1.5 GB (1 GB plans) need swap and smaller PostgreSQL and worker
# settings; nominal 2 GB plans report about 1.9-2.0 GB and are left alone.
small_host() { [ "$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)" -lt 1500000 ]; }

add_swap_if_needed() {
  swap_kb="$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)"
  if small_host && [ "$swap_kb" -eq 0 ] && [ ! -e /swapfile ]; then
    log "small host without swap: creating a 2 GB swap file"
    if fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile; then
      echo '/swapfile none swap sw 0 0' >> /etc/fstab
    else
      rm -f /swapfile
      log "could not enable swap; continuing without it"
    fi
  fi
}

# On a fresh VM, apt-daily and unattended-upgrades often hold the apt locks during
# first boot. Wait for them, and retry because not every apt lock honours a timeout.
apt_busy() {
  pgrep -x apt-get >/dev/null 2>&1 || pgrep -x apt >/dev/null 2>&1 ||
    pgrep -x dpkg >/dev/null 2>&1 || pgrep -f 'apt\.systemd\.daily' >/dev/null 2>&1 ||
    # The upgrade run itself, not the always-running unattended-upgrade-shutdown daemon.
    pgrep -f '/usr/bin/unattended-upgrade( |$)' >/dev/null 2>&1
}

wait_for_apt() {
  waited=0
  while apt_busy; do
    [ "$waited" -eq 0 ] && log "waiting for other apt/dpkg processes to finish"
    [ "$waited" -ge 900 ] && fail "apt/dpkg stayed busy for 15 minutes"
    sleep 5
    waited=$((waited + 5))
  done
}

apt_get() {
  attempt=1
  while :; do
    wait_for_apt
    if apt-get -o DPkg::Lock::Timeout=300 "$@"; then
      return 0
    fi
    [ "$attempt" -ge 30 ] && return 1
    attempt=$((attempt + 1))
    log "apt-get $1 failed; retrying ($attempt/30)"
    sleep 10
  done
}

install_packages() {
  log "installing base packages"
  export DEBIAN_FRONTEND=noninteractive
  # get.docker.com runs its own apt-get commands; give them the same lock timeout.
  echo 'DPkg::Lock::Timeout "300";' > /etc/apt/apt.conf.d/80iapstack-lock-timeout
  apt_get update -q
  apt_get install -y -q ca-certificates curl git openssl cron procps >/dev/null

  if ! command -v docker >/dev/null 2>&1; then
    log "installing Docker Engine from get.docker.com"
    wait_for_apt
    curl -fsSL https://get.docker.com | sh
  fi
  if ! docker compose version >/dev/null 2>&1; then
    # Docker's repository calls the plugin docker-compose-plugin; Ubuntu's archive,
    # used with the docker.io package, calls it docker-compose-v2.
    apt_get install -y -q docker-compose-plugin >/dev/null 2>&1 ||
      apt_get install -y -q docker-compose-v2 >/dev/null ||
      fail "could not install the Docker Compose plugin"
  fi
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    systemctl enable --now docker >/dev/null 2>&1 || true
    systemctl enable --now cron >/dev/null 2>&1 || true
  fi
  docker info >/dev/null 2>&1 || fail "Docker is installed but the daemon is not reachable"
}

stack_running() {
  [ -f "$IAPSTACK_DIR/deploy/vps/.env" ] &&
    (cd "$IAPSTACK_DIR/deploy/vps" && docker compose ps --status running --services 2>/dev/null) |
    grep -qx postgres
}

# Fetches IAPSTACK_REF and moves the checkout to it, taking a backup first when a
# running stack changes commits. Without IAPSTACK_REF an existing checkout stays put.
checkout_source() {
  if [ ! -d "$IAPSTACK_DIR/.git" ]; then
    log "cloning $IAPSTACK_REPO into $IAPSTACK_DIR"
    mkdir -p "$IAPSTACK_DIR"
    git -C "$IAPSTACK_DIR" init -q
    git -C "$IAPSTACK_DIR" remote add origin "$IAPSTACK_REPO"
  fi
  current="$(git -C "$IAPSTACK_DIR" rev-parse -q --verify HEAD 2>/dev/null || echo none)"

  if [ -z "$IAPSTACK_REF" ] && [ "$current" != none ]; then
    log "staying on deployed commit $(git -C "$IAPSTACK_DIR" rev-parse --short HEAD) (set IAPSTACK_REF to change it)"
    return 0
  fi

  ref="${IAPSTACK_REF:-main}"
  log "fetching $ref"
  git -C "$IAPSTACK_DIR" fetch -q --depth 1 origin "$ref"
  target="$(git -C "$IAPSTACK_DIR" rev-parse FETCH_HEAD)"
  if [ "$target" = "$current" ]; then
    log "already on $ref ($(git -C "$IAPSTACK_DIR" rev-parse --short HEAD))"
    return 0
  fi

  if stack_running; then
    log "taking a backup before changing commits"
    (cd "$IAPSTACK_DIR/deploy/vps" && ./backup.sh) ||
      fail "pre-upgrade backup failed; the checkout was not changed"
  fi
  git -C "$IAPSTACK_DIR" checkout -q --detach "$target"
  log "deploying $ref at commit $(git -C "$IAPSTACK_DIR" rev-parse --short HEAD)"
}

# A downloaded installer can be older than the commit it deploys (for example a
# CDN-cached copy of a branch). Hand over to the checked-out installer when they differ.
reexec_checked_out_installer() {
  checked_out="$IAPSTACK_DIR/deploy/vps/install.sh"
  [ "${IAPSTACK_INSTALL_REEXEC:-}" = 1 ] && return 0
  [ "$(cksum < "$checked_out")" = "$self_sum" ] && return 0
  log "continuing with the installer from the deployed commit"
  trap - EXIT
  IAPSTACK_INSTALL_REEXEC=1 IAPSTACK_REF="" exec sh "$checked_out"
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
    if small_host; then
      log "small host: applying 1 GB PostgreSQL and worker settings"
      set_env IAPSTACK_POSTGRES_SHARED_BUFFERS 64MB
      set_env IAPSTACK_POSTGRES_MAX_CONNECTIONS 30
      set_env IAPSTACK_WORKER_CONCURRENCY 1
    fi
    if [ -n "${IAPSTACK_BOOTSTRAP_ADMIN_KEY:-}" ]; then
      set_env IAPSTACK_BOOTSTRAP_ADMIN_KEY "$IAPSTACK_BOOTSTRAP_ADMIN_KEY"
    fi
  else
    log "keeping existing .env"
  fi
  if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ]; then
    set_env CLOUDFLARE_TUNNEL_TOKEN "$CLOUDFLARE_TUNNEL_TOKEN"
  fi
  # The connector runs under the "tunnel" Compose profile only when a token exists.
  if [ -n "$(env_value CLOUDFLARE_TUNNEL_TOKEN)" ]; then
    set_env COMPOSE_PROFILES tunnel
  fi
}

start_stack() {
  if [ "$(env_value COMPOSE_PROFILES)" = tunnel ]; then
    log "building and starting PostgreSQL, IAPStack, and the Cloudflare Tunnel"
  else
    log "CLOUDFLARE_TUNNEL_TOKEN is empty: starting without the tunnel"
  fi
  docker compose up --build -d --wait
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
reexec_checked_out_installer
cd "$IAPSTACK_DIR/deploy/vps"
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
