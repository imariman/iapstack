#!/usr/bin/env bash
set -euo pipefail

flutter_version="${FLUTTER_VERSION:?set FLUTTER_VERSION}"
install_directory="${RUNNER_TEMP:?RUNNER_TEMP is required}/flutter"

case "$(uname -s):$(uname -m)" in
  Linux:x86_64)
    release_platform="linux"
    archive_name="flutter_linux_${flutter_version}-stable.tar.xz"
    expected_sha256="a1d8166c0309267cb7dc99f1424eecf08b86946ad3b50723c6f59945964aea45"
    ;;
  Darwin:x86_64)
    release_platform="macos"
    archive_name="flutter_macos_${flutter_version}-stable.zip"
    expected_sha256="21e06435c50be9a43ffea8abb549bd7640cd38197e7741dd780f0680afbb64ba"
    ;;
  Darwin:arm64)
    release_platform="macos"
    archive_name="flutter_macos_arm64_${flutter_version}-stable.zip"
    expected_sha256="38c9ffe0af4a71e4600f4fda310f0e895757550926128e28aa57782ea97538fa"
    ;;
  *)
    echo "Unsupported Flutter runner: $(uname -s) $(uname -m)" >&2
    exit 1
    ;;
esac

flutter_binary="$install_directory/bin/flutter"
if [[ ! -x "$flutter_binary" ]]; then
  archive_path="${RUNNER_TEMP}/${archive_name}"
  curl --fail --location --retry 3 --output "$archive_path" \
    "https://storage.googleapis.com/flutter_infra_release/releases/stable/${release_platform}/${archive_name}"

  if command -v sha256sum >/dev/null 2>&1; then
    actual_sha256="$(sha256sum "$archive_path" | awk '{print $1}')"
  else
    actual_sha256="$(shasum -a 256 "$archive_path" | awk '{print $1}')"
  fi
  if [[ "$actual_sha256" != "$expected_sha256" ]]; then
    echo "Flutter SDK checksum mismatch" >&2
    exit 1
  fi

  case "$archive_name" in
    *.tar.xz) tar -xJf "$archive_path" -C "$RUNNER_TEMP" ;;
    *.zip) unzip -q "$archive_path" -d "$RUNNER_TEMP" ;;
  esac
fi

echo "$install_directory/bin" >> "${GITHUB_PATH:?GITHUB_PATH is required}"
"$flutter_binary" --version
