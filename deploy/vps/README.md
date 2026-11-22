# Deploy IAPStack on a single VM

[`compose.yaml`](compose.yaml) runs the complete stack on one Linux machine:
PostgreSQL, a one-shot migration, one compact `server` process that contains both the
API and the durable worker, and a [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
connector that publishes the API over HTTPS. The VM needs no open inbound ports and
no public IP for application traffic; PostgreSQL and the worker stay private.

This is the lowest-cost always-on topology in this repository. On an always-free VM
it costs nothing beyond a domain name, and on a small paid VPS it costs a few euros per
month. In exchange you own operating-system updates, backups, and uptime.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials until your own release checks pass.

## Why not serverless or scale-to-zero

The worker processes store notifications, delivers signed webhooks, and retries due
reconciliation every minute. On request-driven platforms such as Cloud Run with
request-based billing, CPU is throttled between requests, so that work only progresses
when the next HTTP request arrives. Keeping an instance warm removes the free-tier
benefit. The same per-minute queries keep a scale-to-zero database such as Neon from
suspending, which uses up its monthly compute allowance. A small always-on VM fits this
workload better than a stack of free tiers.

## Choose a host

Allowances below reflect each provider's documentation at the time of writing.
Confirm the live terms before creating resources.

| Host | Cost | Notes |
| --- | --- | --- |
| [Oracle Cloud Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm) Ampere A1 | USD 0 | Up to 4 Arm OCPUs and 24 GB RAM in total, plus 200 GB block storage. Signup needs a card for identity checks. Popular regions can report "Out of capacity" for A1 shapes. Idle Always Free instances can be reclaimed; upgrading the account to Pay As You Go removes that risk, and you are still not charged while you stay within Always Free limits. |
| [Hetzner Cloud](https://www.hetzner.com/cloud/) CAX11 / CX22 | About EUR 4–5/month | 2 vCPU, 4 GB RAM. Predictable billing and fast provisioning. |
| [Google Cloud](https://cloud.google.com/free/docs/free-cloud-features#compute) e2-micro | USD 0–4/month | One Free Tier instance in `us-west1`, `us-central1`, or `us-east1` with 1 GB RAM and 30 GB standard disk. The in-use external IPv4 address may be billed (about USD 3–4/month). The installer applies 1 GB tuning automatically. |
| [DigitalOcean](https://www.digitalocean.com/pricing/droplets) Droplet | USD 6–12/month | 1–2 GB RAM with public IPv4 included. |
| A server you already run | USD 0 | The tunnel works behind NAT, but store notifications need continuous uptime. |

You also need a domain whose DNS is managed by Cloudflare (free plan). Store
notification endpoints must be stable HTTPS URLs on that domain.

Recommended sizing: 1–2 vCPU, 2 GB RAM or more, and 20 GB disk. On Oracle, a
2 OCPU / 12 GB `VM.Standard.A1.Flex` instance leaves room for a second environment.

## Automated install

### Oracle Cloud: one click

[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

The button creates the network and an Always Free Ampere A1 VM through Oracle
Resource Manager, then runs the installer below on first boot. See the
[Oracle guide](../oracle/README.md) for the form fields, outputs, and troubleshooting.

### Google Cloud: Cloud Shell walkthrough

[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

The button opens a walkthrough in Cloud Shell that runs
[`deploy/gcp/deploy.sh`](../gcp/deploy.sh). The script creates a dedicated VPC with
SSH only through Identity-Aware Proxy and a Free Tier `e2-micro`, boots it with the
cloud-init file below, and waits for the installation. See the
[Google Cloud guide](../gcp/README.md).

### Hetzner Cloud or DigitalOcean: one pasted file

Paste [`deploy/vps/cloud-init.yaml`](cloud-init.yaml), with your
tunnel token filled in, into Hetzner's **Cloud config** or a Droplet's **initialization script** field, or
pass it to `hcloud server create --user-data-from-file` or
`doctl compute droplet create --user-data-file`. It runs the installer below on first
boot. See the [Hetzner guide](../hetzner/README.md) and the
[DigitalOcean Droplet guide](../digitalocean/droplet.md) for firewalls, CLI commands,
and post-install steps. The same file works with other providers that accept cloud-init
user data.

### Any Ubuntu or Debian host: one command

[`install.sh`](install.sh) performs the manual steps below on a fresh host. It
installs Docker and base packages, adds swap on hosts with less than 2 GB RAM, checks
out IAPStack into `/opt/iapstack`, generates `.env`, starts the stack, and installs a
daily backup cron entry. Download and review it, then run it as root:

```sh
curl -fsSLO https://raw.githubusercontent.com/imariman/iapstack/main/deploy/vps/install.sh
less install.sh
read -rs CLOUDFLARE_TUNNEL_TOKEN && export CLOUDFLARE_TUNNEL_TOKEN   # paste, then Enter
sudo --preserve-env=CLOUDFLARE_TUNNEL_TOKEN IAPSTACK_REF=main sh install.sh
```

Reading the token with `read -s` keeps it out of shell history and the process list.

Set `IAPSTACK_REF` to a release tag or the full 40-character SHA of the commit you
have tested. Without
`CLOUDFLARE_TUNNEL_TOKEN`, the stack starts without the tunnel; add the token to `.env`
later and run the script again. Re-running it is also the upgrade path: with
`IAPSTACK_REF` it backs up the database and moves to that ref; without it, it stays on
the deployed commit. Either way it keeps the existing `.env` and restarts the stack. Create the tunnel as in step 4 before running it, and
continue with steps 6–8 afterwards.

After an installer run, `curl https://iapstack.example.com/readyz` shows the deployed
build: `commit` is the full SHA of the checkout, and `version` is the `v…` release tag that
points at it, or `dev` for an untagged commit. A hand-run `docker compose up --build`
reports the same only when you pass `IAPSTACK_COMMIT` and `IAPSTACK_VERSION`, as the
manual steps below do.

## Manual deploy

1. **Create the VM.** Use Ubuntu 24.04 LTS (Arm64 on Oracle A1 and Hetzner CAX,
   x86_64 elsewhere) with SSH key authentication. Allow inbound SSH only, ideally
   restricted to your IP address. Ports 80 and 443 stay closed because the tunnel
   connects outbound. On Oracle, set a budget alert after upgrading to Pay As You Go.

2. **Prepare the host.**

   ```sh
   sudo apt-get update && sudo apt-get -y upgrade
   sudo apt-get install -y git openssl unattended-upgrades
   curl -fsSL https://get.docker.com | sudo sh
   sudo usermod -aG docker "$USER"   # log out and back in afterwards
   ```

   Review the [Docker installation guide](https://docs.docker.com/engine/install/ubuntu/)
   if you prefer the apt repository over the convenience script.

3. **Clone IAPStack and generate secrets.** Check out a release tag or the commit
   you have tested rather than an arbitrary branch head.

   ```sh
   sudo mkdir -p /opt/iapstack && sudo chown "$USER" /opt/iapstack
   git clone https://github.com/imariman/iapstack.git /opt/iapstack
   cd /opt/iapstack/deploy/vps
   ./init-env.sh
   ```

   `init-env.sh` writes `.env` with mode 600 and refuses to overwrite an existing
   file. Copy the generated values to your password manager. The PostgreSQL password
   and protection keys must stay the same for the lifetime of the database volume.

4. **Create the tunnel.** In Cloudflare Zero Trust, open **Networks → Tunnels**,
   create a `cloudflared` tunnel, copy its connector token into
   `CLOUDFLARE_TUNNEL_TOKEN` in `.env`, and set `COMPOSE_PROFILES=tunnel` so Compose
   starts the connector. Add a public hostname such as
   `iapstack.example.com` with service type `HTTP` and URL `iapstack:8080`.

5. **Start the stack.** The first build compiles IAPStack on the VM and takes a few
   minutes.

   ```sh
   IAPSTACK_COMMIT="$(git rev-parse HEAD)" docker compose up --build -d --wait
   curl --fail http://127.0.0.1:8080/readyz
   curl --fail https://iapstack.example.com/readyz
   ```

   `IAPSTACK_COMMIT` stamps the checkout's commit into the image so `/readyz` reports
   it; add `IAPSTACK_VERSION=<tag>` when you deploy a release tag.

6. **Create a durable administrator.** Open `https://iapstack.example.com/dashboard/`,
   choose **Use an admin or bootstrap key instead**, connect with
   `IAPSTACK_BOOTSTRAP_ADMIN_KEY`, and fill in a username and password under **Create
   account** in the setup banner. Your browser session upgrades automatically and the
   bootstrap key retires immediately without restarting the stack (you can still clear
   `IAPSTACK_BOOTSTRAP_ADMIN_KEY=` in `.env` later as cleanup). From then on you sign in
   with that username and password; save them in your password manager. If you forget
   the password, run `docker compose exec iapstack /usr/local/bin/iapstack admin reset-password <username>`
   on the server.

7. **Configure stores.** Create the project and application in the dashboard, then
   register the notification URLs described in the
   [operations guide](../../docs/operations.md) using your tunnel hostname, for
   example
   `https://iapstack.example.com/v1/providers/huawei/projects/<project_id>/applications/<application_id>/notifications`.

8. **Protect the edge.** In the Cloudflare dashboard:

   - Add a rate-limiting rule for `/v1/providers/` paths. Huawei order notifications
     are not independently signed, so the operations guide asks for a bounded rate
     limit at the public proxy. Do not enable features that rewrite request bodies.
   - If you serve public offering documents under `deploy/vps/offerings/`, also
     rate-limit or edge-cache `/v1/applications/*/offering`. The route is
     unauthenticated, reads from disk, and queries `store_products` per request;
     while IAPStack returns `Cache-Control: public, max-age=300` and an `ETag`,
     Cloudflare does not cache `application/json` responses by default.
   - Optionally put `/dashboard*` behind Cloudflare Access so only your identity can
     reach the dashboard login.

## Backups

[`backup.sh`](backup.sh) writes a custom-format `pg_dump` to `deploy/vps/backups/`,
checks that `pg_restore` can read it, and deletes local copies older than
`IAPSTACK_BACKUP_KEEP_DAYS` (default 14). A backup that stays on the same VM does not
survive losing the VM, so also configure an off-server target:

1. Install [rclone](https://rclone.org/install/) and configure a remote for an object
   store such as [Cloudflare R2](https://developers.cloudflare.com/r2/pricing/) (10 GB
   free) or Backblaze B2.
2. Wrap it in an [rclone crypt](https://rclone.org/crypt/) remote so dumps are
   encrypted before upload, and store the crypt passwords with your other secrets.
3. Set `IAPSTACK_BACKUP_RCLONE_TARGET=<crypt-remote>:` in `.env`.
4. Schedule the script from the host crontab (`crontab -e`):

   ```cron
   17 3 * * * /opt/iapstack/deploy/vps/backup.sh >> /opt/iapstack/deploy/vps/backup.log 2>&1
   ```

Rehearse a restore into a separate database as described in
[Backup and restore rehearsal](../../docs/operations.md#backup-and-restore-rehearsal)
before relying on the backups. Keep copies of `.env` and any
`deploy/vps/offerings/*.json` files off-server (or in your own repository), because
database dumps do not include them.

## Monitoring

- Point a free uptime monitor at `https://iapstack.example.com/readyz` and alert on
  failures. The endpoint fails when PostgreSQL is unavailable.
- `docker compose ps` and `docker compose logs --tail=200 iapstack` show local state.
- Set the optional `IAPSTACK_GRAFANA_OTLP_*` values to export metrics to Grafana Cloud
  and import the [dashboard and alerts](../grafana/README.md).

## Application offerings

The `iapstack` service mounts `deploy/vps/offerings` read-only at
`/etc/iapstack/offerings` (`IAPSTACK_OFFERING_DIRECTORY`). To serve a public offering
document at `GET /v1/applications/<application_id>/offering`, place a matching
`<application_id>.json` file in `deploy/vps/offerings/` with traversable directory and
world-readable file permissions (`chmod 755 deploy/vps/offerings` and `chmod 644`, since
the container runs as UID `65532`; `install.sh` repairs both automatically). The route
is unauthenticated and queries `store_products` per request, so cover
`/v1/applications/*/offering` in Cloudflare rate-limiting or cache rules alongside Step 8,
and back up `deploy/vps/offerings/*.json` off-server with `.env`. See
[`offerings/README.md`](offerings/README.md) for the full validation rules and JSON
schema.

## Upgrades

Review the release notes, then re-run the installer with the new tag. It backs up the
database before changing commits, uses the installer from the new commit, keeps
`.env`, and restarts the stack:

```sh
sudo IAPSTACK_REF=<tag> sh /opt/iapstack/deploy/vps/install.sh
```

To upgrade by hand instead, take a backup, check out the tag, and rebuild:

```sh
cd /opt/iapstack
./deploy/vps/backup.sh
git fetch --depth 1 origin tag <tag> && git checkout --detach <tag>
cd deploy/vps
IAPSTACK_COMMIT="$(git rev-parse HEAD)" IAPSTACK_VERSION=<tag> docker compose up --build -d --wait
docker image prune -f
```

Without `IAPSTACK_COMMIT` and `IAPSTACK_VERSION`, the rebuilt image reports
`"version":"dev"` and no commit on `/readyz`.

The `migrate` service applies schema changes before the server restarts. Follow the
rollback procedure in the operations guide if a release fails.

## Scaling out

When one process is no longer enough, split `server` into separate `api` and `worker`
services as in [`../compose.yaml`](../compose.yaml), or move PostgreSQL to a managed
service by changing `IAPSTACK_DATABASE_URL`. Neither change requires application code
changes.

## Cleanup

```sh
cd /opt/iapstack/deploy/vps
docker compose down          # add --volumes to delete the database permanently
```

Delete the tunnel and its DNS record in Cloudflare, remove off-server backups you no
longer need, and terminate the VM and its boot volume at the provider.
