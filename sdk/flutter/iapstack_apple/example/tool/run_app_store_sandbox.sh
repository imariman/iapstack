#!/usr/bin/env bash

set -euo pipefail

readonly base_url="https://iapstack-sandbox.onrender.com"
readonly application_id="ios-sandbox"
readonly external_customer_id="663c43ca-1022-4750-b997-ccbb56957abc"
readonly keychain_service="IAPStack Sandbox Application Key"
readonly keychain_account="iapstack-example:ios-sandbox"

for command_name in curl flutter jq security; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'Required command is unavailable: %s\n' "$command_name" >&2
    exit 1
  fi
done

device_id="${1:-}"
if [[ -z "$device_id" ]]; then
  device_id="$({ flutter devices --machine 2>/dev/null || true; } | jq -r '
    [.[] | select(.targetPlatform == "ios" and .emulator == false)][0].id // empty
  ' 2>/dev/null || true)"
fi
if [[ -z "$device_id" ]]; then
  printf 'Connect and trust a physical iPhone, then run this command again.\n' >&2
  exit 1
fi

application_key="$(security find-generic-password \
  -s "$keychain_service" \
  -a "$keychain_account" \
  -w)"

session_response="$(curl --silent --show-error --fail-with-body \
  --request POST \
  --url "$base_url/v1/applications/$application_id/customer-sessions" \
  --header "Authorization: Bearer $application_key" \
  --header 'Content-Type: application/json' \
  --data-binary "{\"external_customer_id\":\"$external_customer_id\"}")"

customer_token="$(printf '%s' "$session_response" | jq -er '.token')"
expires_at="$(printf '%s' "$session_response" | jq -er '.expires_at')"

unset application_key session_response
printf 'Starting App Store sandbox session; token expires at %s.\n' "$expires_at"

exec flutter run \
  --device-id "$device_id" \
  --dart-define="IAPSTACK_BASE_URL=$base_url" \
  --dart-define="IAPSTACK_APPLICATION_ID=$application_id" \
  --dart-define="IAPSTACK_CUSTOMER_TOKEN=$customer_token" \
  --dart-define="IAPSTACK_EXTERNAL_CUSTOMER_ID=$external_customer_id" \
  --dart-define='IAPSTACK_APPLE_SUBSCRIPTION_ID=' \
  --dart-define='IAPSTACK_APPLE_NON_CONSUMABLE_ID=premium_lifetime'
