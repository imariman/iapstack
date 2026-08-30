#!/usr/bin/env bash

set -euo pipefail

readonly APP_NAME="${IAPSTACK_KOYEB_APP:-iapstack-sandbox}"
readonly DATABASE_NAME="${IAPSTACK_KOYEB_DATABASE:-postgres}"
readonly REGION="${IAPSTACK_KOYEB_REGION:-fra}"
readonly GIT_REPOSITORY="${IAPSTACK_GIT_REPOSITORY:-github.com/imariman/iapstack}"
readonly GIT_BRANCH="${IAPSTACK_GIT_BRANCH:-main}"
readonly DATABASE_INSTANCE="${IAPSTACK_KOYEB_DATABASE_INSTANCE:-free}"
readonly API_INSTANCE="${IAPSTACK_KOYEB_API_INSTANCE:-free}"
readonly WORKER_INSTANCE="${IAPSTACK_KOYEB_WORKER_INSTANCE:-eco-micro}"
readonly SECRET_PREFIX="${IAPSTACK_KOYEB_SECRET_PREFIX:-${APP_NAME}}"

readonly DATABASE_URL_SECRET="${SECRET_PREFIX}-database-url"
readonly PROTECTION_KEY_SECRET="${SECRET_PREFIX}-protection-key"
readonly FINGERPRINT_KEY_SECRET="${SECRET_PREFIX}-fingerprint-key"
readonly BOOTSTRAP_KEY_SECRET="${SECRET_PREFIX}-bootstrap-admin-key"
readonly METRICS_KEY_SECRET="${SECRET_PREFIX}-metrics-bearer-token"

for command_name in koyeb jq openssl go; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "${command_name}" >&2
    exit 1
  fi
done

if [[ ! -f Dockerfile || ! -f go.mod ]]; then
  printf 'Run this script from the IAPStack repository root.\n' >&2
  exit 1
fi

if ! koyeb whoami >/dev/null 2>&1; then
  printf 'Authenticate first with `koyeb login` or configure KOYEB_TOKEN.\n' >&2
  exit 1
fi

secret_exists() {
  koyeb secret get "$1" >/dev/null 2>&1
}

create_secret_once() {
  local secret_name="$1"
  local secret_value="$2"

  if secret_exists "${secret_name}"; then
    printf 'Keeping existing Koyeb secret: %s\n' "${secret_name}"
    return
  fi

  printf '%s' "${secret_value}" | koyeb secret create "${secret_name}" --value-from-stdin >/dev/null
  printf 'Created Koyeb secret: %s\n' "${secret_name}"
}

upsert_secret() {
  local secret_name="$1"
  local secret_value="$2"

  if secret_exists "${secret_name}"; then
    printf '%s' "${secret_value}" | koyeb secret update "${secret_name}" --value-from-stdin >/dev/null
    printf 'Updated Koyeb secret: %s\n' "${secret_name}"
    return
  fi

  printf '%s' "${secret_value}" | koyeb secret create "${secret_name}" --value-from-stdin >/dev/null
  printf 'Created Koyeb secret: %s\n' "${secret_name}"
}

protection_key="$(openssl rand -base64 32 | tr -d '\n')"
fingerprint_key="$(openssl rand -base64 32 | tr -d '\n')"
bootstrap_key="$(openssl rand -base64 32 | tr -d '\n')"
metrics_key="$(openssl rand -base64 32 | tr -d '\n')"

create_secret_once "${PROTECTION_KEY_SECRET}" "${protection_key}"
create_secret_once "${FINGERPRINT_KEY_SECRET}" "${fingerprint_key}"
create_secret_once "${METRICS_KEY_SECRET}" "${metrics_key}"

bootstrap_key_created=false
if ! secret_exists "${BOOTSTRAP_KEY_SECRET}"; then
  create_secret_once "${BOOTSTRAP_KEY_SECRET}" "${bootstrap_key}"
  bootstrap_key_created=true
else
  printf 'Keeping existing Koyeb secret: %s\n' "${BOOTSTRAP_KEY_SECRET}"
fi

if koyeb database get "${APP_NAME}/${DATABASE_NAME}" >/dev/null 2>&1; then
  printf 'Keeping existing Koyeb database: %s/%s\n' "${APP_NAME}" "${DATABASE_NAME}"
else
  koyeb database create "${DATABASE_NAME}" \
    --app "${APP_NAME}" \
    --instance-type "${DATABASE_INSTANCE}" \
    --pg-version 16 \
    --region "${REGION}" \
    --db-name iapstack \
    --db-owner iapstack
fi

database_url=""
for _ in {1..60}; do
  database_json="$(koyeb database get "${APP_NAME}/${DATABASE_NAME}" --output json 2>/dev/null || true)"
  database_url="$(printf '%s' "${database_json}" | jq -r '.ConnectionStrings[0] // .connection_strings[0] // empty' 2>/dev/null || true)"
  if [[ -n "${database_url}" ]]; then
    break
  fi
  sleep 5
done

if [[ -z "${database_url}" ]]; then
  printf 'The Koyeb database did not become ready within five minutes. Re-run the script after checking its status.\n' >&2
  exit 1
fi

case "${database_url}" in
  *sslmode=*) ;;
  *\?*) database_url="${database_url}&sslmode=require" ;;
  *) database_url="${database_url}?sslmode=require" ;;
esac

upsert_secret "${DATABASE_URL_SECRET}" "${database_url}"

printf 'Applying database migrations from the local checkout...\n'
IAPSTACK_DATABASE_URL="${database_url}" go run ./cmd/iapstack migrate

common_service_flags=(
  --git "${GIT_REPOSITORY}"
  --git-branch "${GIT_BRANCH}"
  --git-builder docker
  --git-docker-dockerfile Dockerfile
  --git-no-deploy-on-push
  --regions "${REGION}"
  --env "IAPSTACK_DATABASE_URL={{secret.${DATABASE_URL_SECRET}}}"
  --env "IAPSTACK_PROTECTION_ACTIVE_KEY_ID=primary"
  --env "IAPSTACK_PROTECTION_KEY={{secret.${PROTECTION_KEY_SECRET}}}"
  --env "IAPSTACK_PROTECTION_FINGERPRINT_KEY={{secret.${FINGERPRINT_KEY_SECRET}}}"
  --env "IAPSTACK_METRICS_BEARER_TOKEN={{secret.${METRICS_KEY_SECRET}}}"
)

deploy_service() {
  local service_name="$1"
  local service_type="$2"
  local command_name="$3"
  local instance_type="$4"
  shift 4

  local service_flags=(
    "${common_service_flags[@]}"
    --type "${service_type}"
    --git-docker-command "${command_name}"
    --instance-type "${instance_type}"
    "$@"
  )

  if koyeb service get "${APP_NAME}/${service_name}" >/dev/null 2>&1; then
    koyeb service update "${APP_NAME}/${service_name}" --override "${service_flags[@]}" --wait
  else
    koyeb service create "${service_name}" --app "${APP_NAME}" "${service_flags[@]}" --wait
  fi
}

deploy_service api web api "${API_INSTANCE}" \
  --ports 8080:http \
  --routes /:8080 \
  --checks 8080:http:/readyz \
  --checks-grace-period 8080=30 \
  --env "IAPSTACK_BOOTSTRAP_ADMIN_KEY={{secret.${BOOTSTRAP_KEY_SECRET}}}" \
  --env IAPSTACK_HTTP_ADDRESS=:8080

deploy_service worker worker worker "${WORKER_INSTANCE}" \
  --env IAPSTACK_WORKER_HTTP_ADDRESS=:8081

printf '\nKoyeb deployment created or updated: %s\n' "${APP_NAME}"
printf 'Automatic deploy-on-push is disabled. Re-run this script for deliberate updates.\n'
if [[ "${bootstrap_key_created}" == true ]]; then
  printf '\nOne-time bootstrap administrator key (store it securely now):\n%s\n' "${bootstrap_key}"
else
  printf '\nThe existing bootstrap key was not revealed or rotated.\n'
fi
