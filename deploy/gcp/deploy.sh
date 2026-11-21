#!/usr/bin/env bash
# Creates an IAPStack single-VM deployment on a Google Cloud e2-micro instance.
#
# Run it in Cloud Shell or anywhere gcloud is authenticated:
#   bash deploy/gcp/deploy.sh
#
# Settings (environment variables, all optional):
#   GOOGLE_CLOUD_PROJECT     Project ID (default: the active gcloud project)
#   IAPSTACK_ZONE            Zone in us-west1, us-central1, or us-east1 (default: us-central1-a)
#   IAPSTACK_INSTANCE        Instance name (default: iapstack)
#   IAPSTACK_MACHINE_TYPE    Machine type (default: e2-micro, the Free Tier type)
#   IAPSTACK_REF             IAPStack Git tag, branch, or full commit SHA (default: main)
#   CLOUDFLARE_TUNNEL_TOKEN  Tunnel connector token; prompted for when unset in a terminal
#   IAPSTACK_DRY_RUN=1       Print the gcloud commands instead of running them
#
# The script creates a dedicated VPC whose only inbound rule allows SSH from Google's
# Identity-Aware Proxy range, then boots Ubuntu 24.04 with deploy/vps/cloud-init.yaml.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cloud_init_source="$script_dir/../vps/cloud-init.yaml"

project="${GOOGLE_CLOUD_PROJECT:-$(gcloud config get-value project 2>/dev/null || true)}"
zone="${IAPSTACK_ZONE:-us-central1-a}"
region="${zone%-*}"
instance="${IAPSTACK_INSTANCE:-iapstack}"
machine_type="${IAPSTACK_MACHINE_TYPE:-e2-micro}"
ref="${IAPSTACK_REF:-main}"
dry_run="${IAPSTACK_DRY_RUN:-0}"
network="iapstack"
subnet="iapstack-$region"
iap_range="35.235.240.0/20"

log() { printf '\n[iapstack-gcp] %s\n' "$*"; }
fail() { printf '[iapstack-gcp] ERROR: %s\n' "$*" >&2; exit 1; }
run() {
  if [[ "$dry_run" == "1" ]]; then
    printf '+ %s\n' "$*"
  else
    "$@"
  fi
}
# exists <gcloud args...> succeeds when the described resource already exists.
exists() { [[ "$dry_run" != "1" ]] && "$@" >/dev/null 2>&1; }

[[ -n "$project" ]] || fail "set GOOGLE_CLOUD_PROJECT or run: gcloud config set project <project-id>"
[[ -f "$cloud_init_source" ]] || fail "missing $cloud_init_source; run from an IAPStack checkout"
[[ "$ref" =~ ^[A-Za-z0-9._/-]+$ ]] || fail "IAPSTACK_REF contains unsupported characters"
case "$region" in
  us-west1 | us-central1 | us-east1) ;;
  *) log "WARNING: $region is outside the e2-micro Free Tier regions (us-west1, us-central1, us-east1)." ;;
esac
[[ "$machine_type" == "e2-micro" ]] || log "WARNING: $machine_type is not covered by the Free Tier."

if [[ -z "${CLOUDFLARE_TUNNEL_TOKEN:-}" && -t 0 && "$dry_run" != "1" ]]; then
  printf 'Cloudflare Tunnel token (leave empty to add it later): '
  read -rs CLOUDFLARE_TUNNEL_TOKEN
  printf '\n'
fi
token="${CLOUDFLARE_TUNNEL_TOKEN:-}"
[[ "$token" =~ ^[A-Za-z0-9+/=_-]*$ ]] || fail "the tunnel token contains unexpected characters"

user_data="$(mktemp)"
trap 'rm -f "$user_data"' EXIT
chmod 600 "$user_data"
sed -e "s|^      IAPSTACK_REF=.*|      IAPSTACK_REF='$ref'|" \
  -e "s|^      CLOUDFLARE_TUNNEL_TOKEN=.*|      CLOUDFLARE_TUNNEL_TOKEN='$token'|" \
  "$cloud_init_source" > "$user_data"

log "project $project, zone $zone, instance $instance ($machine_type), ref $ref"

log "enabling the Compute Engine API"
run gcloud services enable compute.googleapis.com --project "$project"

if exists gcloud compute networks describe "$network" --project "$project"; then
  log "network $network already exists"
else
  log "creating network $network"
  run gcloud compute networks create "$network" --project "$project" --subnet-mode=custom
fi

if exists gcloud compute networks subnets describe "$subnet" --region "$region" --project "$project"; then
  log "subnet $subnet already exists"
else
  log "creating subnet $subnet"
  run gcloud compute networks subnets create "$subnet" --project "$project" \
    --network "$network" --region "$region" --range 10.42.0.0/24
fi

if exists gcloud compute firewall-rules describe iapstack-allow-iap-ssh --project "$project"; then
  log "firewall rule iapstack-allow-iap-ssh already exists"
else
  log "allowing SSH only from Identity-Aware Proxy ($iap_range)"
  run gcloud compute firewall-rules create iapstack-allow-iap-ssh --project "$project" \
    --network "$network" --direction INGRESS --allow tcp:22 --source-ranges "$iap_range"
fi

if exists gcloud compute instances describe "$instance" --zone "$zone" --project "$project"; then
  fail "instance $instance already exists in $zone; upgrade it with install.sh over SSH instead"
fi

log "creating instance $instance"
run gcloud compute instances create "$instance" --project "$project" --zone "$zone" \
  --machine-type "$machine_type" \
  --subnet "$subnet" \
  --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud \
  --boot-disk-size 30GB --boot-disk-type pd-standard \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata-from-file user-data="$user_data"

if [[ "$dry_run" == "1" ]]; then
  log "dry run complete; rendered user data:"
  sed "s|CLOUDFLARE_TUNNEL_TOKEN='[^']*'|CLOUDFLARE_TUNNEL_TOKEN='<redacted>'|" "$user_data"
  exit 0
fi

log "waiting for the first-boot installation (usually 10-20 minutes on e2-micro)"
deadline=$((SECONDS + 45 * 60))
while ((SECONDS < deadline)); do
  output="$(gcloud compute instances get-serial-port-output "$instance" --zone "$zone" \
    --project "$project" 2>/dev/null || true)"
  if grep -q '\[iapstack-install\] IAPStack is running\.' <<<"$output"; then
    log "IAPStack is running"
    break
  fi
  if grep -q '\[iapstack-install\] ERROR' <<<"$output"; then
    grep '\[iapstack-install\]' <<<"$output" | tail -n 5
    fail "installation failed; inspect: gcloud compute instances get-serial-port-output $instance --zone $zone"
  fi
  printf '.'
  sleep 30
done
((SECONDS < deadline)) || fail "installation did not finish within 45 minutes; check the serial port output"

ssh_command="gcloud compute ssh $instance --zone $zone --project $project --tunnel-through-iap"
printed_bootstrap=0
if [[ -t 0 ]]; then
  printf '\nRead the one-time bootstrap administrator key now? [y/N]: '
  read -r answer
  if [[ "$answer" =~ ^[Yy]$ ]]; then
    if "$ssh_command" --command 'sudo grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= /opt/iapstack/deploy/vps/.env'; then
      printed_bootstrap=1
    fi
  fi
fi

cat <<EOF

Next steps:

EOF
if [[ "$printed_bootstrap" == "0" ]]; then
  cat <<EOF
1. Read the one-time bootstrap administrator key:
   $ssh_command --command 'sudo grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= /opt/iapstack/deploy/vps/.env'
2. Open https://<your-tunnel-hostname>/dashboard/, sign in with that key, and follow the
   banner to create your permanent admin key (your session switches automatically and the
   bootstrap key retires immediately with no server restart).
3. Save the permanent key in your password manager and back up /opt/iapstack/deploy/vps/.env
   (deploy/vps/README.md).
EOF
else
  cat <<EOF
1. Open https://<your-tunnel-hostname>/dashboard/, sign in with the bootstrap key above, and
   follow the banner to create your permanent admin key (your session switches automatically
   and the bootstrap key retires immediately with no server restart).
2. Save the permanent key in your password manager and back up /opt/iapstack/deploy/vps/.env
   (deploy/vps/README.md).
EOF
fi
