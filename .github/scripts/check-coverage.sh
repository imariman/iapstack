#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 4 ]]; then
  echo "usage: check-coverage.sh <go|lcov> <profile> <minimum-percent> <label>" >&2
  exit 2
fi

coverage_format="$1"
coverage_profile="$2"
minimum_percent="$3"
coverage_label="$4"

if [[ ! -s "$coverage_profile" ]]; then
  echo "$coverage_label coverage profile is missing or empty: $coverage_profile" >&2
  exit 1
fi
if [[ ! "$minimum_percent" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "invalid minimum coverage: $minimum_percent" >&2
  exit 2
fi

case "$coverage_format" in
  go)
    actual_percent="$(
      go tool cover -func="$coverage_profile" |
        awk '$1 == "total:" {gsub(/%/, "", $3); print $3}'
    )"
    ;;
  lcov)
    actual_percent="$(
      awk -F: '
        /^LF:/ {found += $2}
        /^LH:/ {hit += $2}
        END {
          if (found == 0) exit 1
          printf "%.1f", (100 * hit / found)
        }
      ' "$coverage_profile"
    )"
    ;;
  *)
    echo "unsupported coverage format: $coverage_format" >&2
    exit 2
    ;;
esac

if [[ ! "$actual_percent" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "$coverage_label coverage could not be determined" >&2
  exit 1
fi

awk -v label="$coverage_label" -v actual="$actual_percent" -v minimum="$minimum_percent" '
  BEGIN {
    printf "%s coverage: %.1f%% (minimum %.1f%%)\n", label, actual, minimum
    if ((actual + 0) < (minimum + 0)) exit 1
  }
'
