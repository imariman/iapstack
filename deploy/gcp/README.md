# Deploy IAPStack to a Google Cloud e2-micro

[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

The button clones IAPStack into Cloud Shell and opens a guided walkthrough. The
walkthrough runs [`deploy.sh`](deploy.sh), which:

- enables the Compute Engine API
- creates a dedicated `iapstack` VPC whose only inbound rule allows SSH from Google's
  [Identity-Aware Proxy](https://cloud.google.com/iap/docs/using-tcp-forwarding) range
- creates an Ubuntu 24.04 `e2-micro` with a 30 GB standard persistent disk
- boots it with [`deploy/vps/cloud-init.yaml`](../vps/cloud-init.yaml), which runs
  [`install.sh`](../vps/install.sh) to start the [single-VM stack](../vps/README.md)
- waits for the installation and prints the remaining steps

You can also run `bash deploy/gcp/deploy.sh` from any checkout where `gcloud` is
authenticated. `IAPSTACK_DRY_RUN=1` prints the commands without creating anything.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials until your own release checks pass.

## Cost and limits

The Google Cloud [Free Tier](https://cloud.google.com/free/docs/free-cloud-features#compute)
covers one non-preemptible `e2-micro` per month in `us-west1`, `us-central1`, or
`us-east1`, 30 GB-months of standard persistent disk, and 1 GB of outbound data
transfer from North America per month. A billing account is required.

Google [bills in-use external IPv4 addresses](https://cloud.google.com/vpc/pricing)
by the hour, roughly USD 3–4 per month, and the Free Tier page does not list them as
covered. The VM needs one for outbound access to GitHub and container registries, so
expect USD 0–4 per month and check the SKUs in your billing report after the first
days.

An `e2-micro` has 1 GB of RAM and a fractional vCPU. The installer adds a 2 GB swap
file and applies small-host PostgreSQL and worker settings automatically. The first
image build takes longer than on larger hosts. This size suits low traffic; move to a
larger machine type when the dashboard or metrics show sustained pressure.

## Secrets

The tunnel token travels in the instance's `user-data` metadata. Anyone with read
access to the instance in the project, and any process on the VM, can read it. Rotate
the token in Cloudflare if that matters to you, or leave it empty during deployment
and add it over SSH. All other secrets, including the bootstrap administrator key, are
generated on the VM and never appear in metadata or the serial console.

## After installation

The script prints the exact commands. In short:

```sh
gcloud compute ssh iapstack --zone us-central1-a --tunnel-through-iap
sudo grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= /opt/iapstack/deploy/vps/.env
```

Create a durable administrator in `/dashboard/`, then remove the bootstrap key:

```sh
cd /opt/iapstack/deploy/vps
sudo sed -i 's/^IAPSTACK_BOOTSTRAP_ADMIN_KEY=.*/IAPSTACK_BOOTSTRAP_ADMIN_KEY=/' .env
sudo docker compose up -d --wait
```

If you skipped the token, add `CLOUDFLARE_TUNNEL_TOKEN=<token>` to `.env` and run
`sudo sh /opt/iapstack/deploy/vps/install.sh`. Then configure off-server backups and
register the store notification URLs as described in the
[single-VM guide](../vps/README.md).

## Upgrades

Connect over IAP SSH and re-run the installer with the target ref. It takes a backup
first, keeps the existing `.env`, and stays on the installed ref when `IAPSTACK_REF` is
omitted:

```sh
sudo IAPSTACK_REF=<tag-or-commit> sh /opt/iapstack/deploy/vps/install.sh
```

## Cleanup

```sh
gcloud compute instances delete iapstack --zone us-central1-a
gcloud compute firewall-rules delete iapstack-allow-iap-ssh
gcloud compute networks subnets delete iapstack-us-central1 --region us-central1
gcloud compute networks delete iapstack
```

Deleting the instance also deletes its boot disk and the database. Delete the tunnel
and its DNS record in Cloudflare, and remove off-server backups you no longer need.
