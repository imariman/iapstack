#!/usr/bin/env bash
# Build the npm archives once; publication uploads these exact reviewed bytes.
set -euo pipefail
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_directory="${1:?Usage: scripts/pack-sdk-npm.sh ABSOLUTE_OUTPUT_DIRECTORY}"
mkdir -p "$output_directory"
output_directory="$(cd "$output_directory" && pwd)"
for package in typescript react-native; do
  (
    cd "$repository_root/sdk/$package"
    bun install --frozen-lockfile
    bun run typecheck
    bun run test
    npm pack --pack-destination "$output_directory"
  )
done
python3 "$repository_root/scripts/check-sdk-packages.py" --npm-dir "$output_directory"

# Exercise the installed host tarball in Node, outside the monorepo and Bun.
consumer_directory="$(mktemp -d "${TMPDIR:-/tmp}/iapstack-host-consumer.XXXXXX")"
trap 'rm -rf "$consumer_directory"' EXIT
version="$(tr -d '\n' < "$repository_root/sdk/version.txt")"
(
  cd "$consumer_directory"
  printf '{"private":true,"type":"module"}\n' > package.json
  npm install --ignore-scripts --legacy-peer-deps --no-audit --no-fund \
    "$output_directory/iapstack-host-$version.tgz" \
    "$output_directory/iapstack-react-native-$version.tgz"
  node --input-type=module -e '
    import { Client, WebhookVerifier, MemoryEventStore } from "@iapstack/host";
    const client = new Client({baseUrl: "https://iap.example", applicationId: "app", applicationToken: "local-package-check"});
    if (Reflect.ownKeys(client).length !== 0 || !WebhookVerifier || !MemoryEventStore) process.exit(1);
  '
  node -e '
    const { IapStackClient, IapStackConfig } = require("@iapstack/react-native");
    const client = new IapStackClient(new IapStackConfig({baseUri: "https://iap.example", applicationId: "app", customerToken: "local-package-check"}));
    if (typeof client.getEntitlements !== "function") process.exit(1);
  '
)
