#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repository_root"

compose_project="${IAPSTACK_E2E_COMPOSE_PROJECT:-iapstack-e2e-${GITHUB_RUN_ID:-$$}}"
certificate_directory="$(mktemp -d "${TMPDIR:-/tmp}/iapstack-e2e.XXXXXX")"
compose_files=(-f deploy/compose.yaml -f deploy/compose.e2e.yaml)

export IAPSTACK_E2E_CERT_DIR="$certificate_directory"
export IAPSTACK_POSTGRES_DB="iapstack_e2e"
export IAPSTACK_POSTGRES_USER="iapstack"
export IAPSTACK_POSTGRES_PASSWORD="${IAPSTACK_POSTGRES_PASSWORD:-$(openssl rand -hex 24)}"
export IAPSTACK_POSTGRES_PORT="${IAPSTACK_POSTGRES_PORT:-15432}"
export IAPSTACK_HTTP_PORT="${IAPSTACK_HTTP_PORT:-18080}"
export IAPSTACK_E2E_WORKER_PORT="${IAPSTACK_E2E_WORKER_PORT:-18081}"
export IAPSTACK_E2E_FIXTURE_PORT="${IAPSTACK_E2E_FIXTURE_PORT:-18082}"
export IAPSTACK_PROTECTION_ACTIVE_KEY_ID="e2e-key"
export IAPSTACK_PROTECTION_KEYS="{\"e2e-key\":\"$(openssl rand -base64 32 | tr -d '\n')\"}"
export IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32 | tr -d '\n')"
export IAPSTACK_BOOTSTRAP_ADMIN_KEY="${IAPSTACK_BOOTSTRAP_ADMIN_KEY:-$(openssl rand -hex 32)}"
export IAPSTACK_E2E_WEBHOOK_SECRET="${IAPSTACK_E2E_WEBHOOK_SECRET:-$(openssl rand -hex 32)}"
export IAPSTACK_E2E_API_BASE_URL="http://127.0.0.1:${IAPSTACK_HTTP_PORT}"
export IAPSTACK_E2E_WORKER_BASE_URL="http://127.0.0.1:${IAPSTACK_E2E_WORKER_PORT}"
export IAPSTACK_E2E_FIXTURE_BASE_URL="http://127.0.0.1:${IAPSTACK_E2E_FIXTURE_PORT}"

cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then
    docker compose -p "$compose_project" "${compose_files[@]}" ps || true
    docker compose -p "$compose_project" "${compose_files[@]}" logs --no-color || true
  fi
  docker compose -p "$compose_project" "${compose_files[@]}" down --volumes --remove-orphans || true
  rm -r -- "$certificate_directory"
  exit "$status"
}
trap cleanup EXIT

openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
  -keyout "$certificate_directory/tls.key" \
  -out "$certificate_directory/tls.crt" \
  -config deploy/e2e/openssl.cnf >/dev/null 2>&1
chmod 0644 "$certificate_directory/tls.key" "$certificate_directory/tls.crt"

docker compose -p "$compose_project" "${compose_files[@]}" up --build --detach --wait
GOCACHE="${GOCACHE:-/tmp/iapstack-e2e-gocache}" go run ./cmd/iapstack-e2e bootstrap

docker compose -p "$compose_project" "${compose_files[@]}" stop worker
GOCACHE="${GOCACHE:-/tmp/iapstack-e2e-gocache}" go run ./cmd/iapstack-e2e enqueue-notification

docker compose -p "$compose_project" "${compose_files[@]}" start worker
GOCACHE="${GOCACHE:-/tmp/iapstack-e2e-gocache}" go run ./cmd/iapstack-e2e assert-recovery

echo "Compose release gate passed"
