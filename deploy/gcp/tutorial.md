# Deploy IAPStack on a Google Cloud e2-micro

<walkthrough-tutorial-duration duration="25"></walkthrough-tutorial-duration>

This walkthrough creates one Ubuntu 24.04 `e2-micro` VM that runs PostgreSQL, the
IAPStack API and worker, and a Cloudflare Tunnel. The VM gets a dedicated network
whose only inbound rule allows SSH through Identity-Aware Proxy.

IAPStack is early-development software. Use isolated store-sandbox credentials first.

## Select a project

Choose a project with billing enabled. The Free Tier covers one `e2-micro` instance and
30 GB of standard disk in `us-west1`, `us-central1`, or `us-east1`. The VM's external
IPv4 address may be billed separately.

<walkthrough-project-setup billing="true"></walkthrough-project-setup>

```sh
gcloud config set project <walkthrough-project-id/>
```

## Create a Cloudflare Tunnel

1. Add your domain to Cloudflare (the free plan is enough).
2. In Cloudflare Zero Trust, open **Networks → Tunnels** and create a `cloudflared`
   tunnel. Copy its token.
3. Add a public hostname such as `iapstack.example.com` with service type `HTTP` and
   URL `iapstack:8080`.

You can skip this step and add the token on the VM later.

## Deploy

Run the deploy script. It asks for the tunnel token without echoing it, creates the
network and the VM, and waits for the first-boot installation, which usually takes
10–20 minutes on an `e2-micro`.

```sh
bash deploy/gcp/deploy.sh
```

To use another Free Tier zone or a pinned IAPStack version, set them first:

```sh
IAPSTACK_ZONE=us-east1-b IAPSTACK_REF=main bash deploy/gcp/deploy.sh
```

## Finish setup

When the script prints **Next steps**, follow them:

1. Read the one-time bootstrap administrator key over IAP SSH (or confirm when `deploy.sh` offers to print it).
2. Open `https://<your-hostname>/dashboard/`, sign in with that key, and follow the setup banner to create your permanent administrator key (your browser session upgrades automatically and the bootstrap key retires immediately).
3. Save the permanent key in your password manager, copy `/opt/iapstack/deploy/vps/.env` to your password manager, and configure off-server backups as described in `deploy/vps/README.md`.

## Done

<walkthrough-conclusion-trophy></walkthrough-conclusion-trophy>

IAPStack is running on your `e2-micro`. See `deploy/gcp/README.md` for upgrades,
costs, and cleanup.
