#!/usr/bin/env bash
# Validate or publish the Flutter SDK packages to pub.dev.
#
#   scripts/publish-sdk-pub.sh --check    validate archives only (CI, no credentials)
#   scripts/publish-sdk-pub.sh --publish  publish sdk/version.txt, core first
#
# Publishing skips versions pub.dev already has, so a failed run can be resumed.
# In GitHub Actions it authenticates with the job's OIDC token and passes
# --force; on a workstation it uses `dart pub login` and pub asks before each
# upload. pub.dev versions cannot be deleted or overwritten.
set -euo pipefail

mode="${1:-}"
case "$mode" in
  --check | --publish) ;;
  *)
    echo "usage: $0 --check | --publish" >&2
    exit 64
    ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
version="$(tr -d '[:space:]' < "$root/sdk/version.txt")"
# Same order as FLUTTER_PACKAGES in check-sdk-packages.py.
packages=(iapstack iapstack_apple iapstack_google_play iapstack_huawei)

python3 "$root/scripts/check-sdk-packages.py" >/dev/null

pub() {
  if [[ "$1" == iapstack ]]; then
    (cd "$root/sdk/flutter/$1" && dart pub "${@:2}")
  else
    (cd "$root/sdk/flutter/$1" && flutter pub "${@:2}")
  fi
}

published_status() {
  curl -sS -o /dev/null -w '%{http_code}' "https://pub.dev/api/packages/$1/versions/$version"
}

refresh_oidc_token() {
  # Each upload gets a fresh token; GitHub OIDC tokens are short-lived.
  PUB_TOKEN="$(curl -fsS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
    "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=https://pub.dev" |
    python3 -c 'import json, sys; print(json.load(sys.stdin)["value"])')"
  echo "::add-mask::$PUB_TOKEN"
  export PUB_TOKEN
}

validate() {
  local name="$1" output status=0
  pub "$name" get >/dev/null
  # Pub archives every file that git does not ignore, tracked or not.
  local untracked
  untracked="$(git -C "$root" ls-files --others --exclude-standard -- "sdk/flutter/$name")"
  if [[ -n "$untracked" ]]; then
    echo "$name: untracked files would be published:" >&2
    echo "$untracked" >&2
    return 1
  fi
  output="$(pub "$name" publish --dry-run 2>&1)" || status=$?
  # Exit 65 also covers warnings, such as the deliberate exact core pin.
  if [[ "$status" -ne 0 && "$status" -ne 65 ]] || grep -Eq 'found the following ([0-9]+ )?errors?:|Sorry, your package is missing some requirements' <<< "$output"; then
    echo "$output" >&2
    echo "$name: pub.dev would reject this package" >&2
    return 1
  fi
  echo "$name $version: archive valid"
}

for name in "${packages[@]}"; do
  validate "$name"
done
[[ "$mode" == --check ]] && exit 0

force=()
if [[ -n "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]]; then
  refresh_oidc_token
  dart pub token add https://pub.dev --env-var PUB_TOKEN
  force=(--force)
fi

for name in "${packages[@]}"; do
  case "$(published_status "$name")" in
    200)
      echo "$name $version is already on pub.dev; skipping"
      continue
      ;;
    404) ;;
    *)
      echo "$name: cannot read pub.dev state" >&2
      exit 1
      ;;
  esac
  [[ -n "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]] && refresh_oidc_token
  # The +-expansion keeps an empty array safe under set -u in macOS bash 3.2.
  pub "$name" publish ${force[@]+"${force[@]}"}
  if [[ "$name" == iapstack ]]; then
    # Companions pin this exact version, so wait until pub.dev serves it.
    for _ in $(seq 1 30); do
      [[ "$(published_status iapstack)" == 200 ]] && break
      sleep 10
    done
    [[ "$(published_status iapstack)" == 200 ]] || {
      echo "iapstack $version did not appear on pub.dev" >&2
      exit 1
    }
  fi
done
echo "Flutter SDK $version is on pub.dev"
