# Deploy IAPStack to Hetzner Cloud

Hetzner Cloud has no deploy button, but a server can run a cloud-init file on first
boot. [`cloud-init.yaml`](../vps/cloud-init.yaml) runs [`deploy/vps/install.sh`](../vps/install.sh),
which installs Docker and starts the [single-VM stack](../vps/README.md): PostgreSQL,
migrations, the compact API and worker process, and the Cloudflare Tunnel connector.
Creating the server and pasting one file is the whole deployment.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials until your own release checks pass.

## Cost

A shared-vCPU server with 2 vCPUs and 4 GB RAM, such as the Arm `CAX11`, costs about
EUR 4–5 per month including its public IPv4 address. Arm types are offered only in
Hetzner's EU locations; x86 shared types work the same way. Hetzner's optional server
backups add 20% to the server price. Check the
[live prices](https://www.hetzner.com/cloud/) before creating the server.

Keep public IPv4 enabled. GitHub, which serves the installer and the source code,
is not reachable over IPv6 only.

## Before you start

1. **Cloudflare Tunnel.** Add your domain to Cloudflare (the free plan is enough).
   In Zero Trust, open **Networks → Tunnels**, create a `cloudflared` tunnel, and copy
   its token. Add a public hostname such as `iapstack.example.com` with service type
   `HTTP` and URL `iapstack:8080`.
2. **Cloud config.** Download [`cloud-init.yaml`](../vps/cloud-init.yaml) and paste the token
   between the quotes of `CLOUDFLARE_TUNNEL_TOKEN`. Set `IAPSTACK_REF` to a release tag
   or a commit you have tested. You can leave the token empty and add it later over
   SSH.

The cloud config is stored as the server's user data and stays readable from the
metadata service by any process on the server. The installer deletes its own copy of
the token file, but treat the token as exposed to the server and to the Hetzner
project. Rotate the token in Cloudflare if that matters to you, or leave it empty here
and add it over SSH. All other secrets
are generated on the server and never appear in the cloud config.

## Deploy with the Hetzner Console

1. In your project, open **Firewalls → Create Firewall**. Keep one inbound rule for
   TCP port 22 limited to your own IP address, and remove every other inbound rule.
   The tunnel only makes outbound connections, so no other port needs to be open.
2. Open **Servers → Add Server** and choose:
   - **Location:** any; Arm types are EU-only
   - **Image:** Ubuntu 24.04
   - **Type:** `CAX11` or another type with at least 2 GB RAM
   - **Networking:** public IPv4 on
   - **SSH keys:** your key
   - **Firewalls:** the firewall from step 1
   - **Cloud config:** paste your edited `cloud-init.yaml`
3. Create the server. Installation continues for 5–10 minutes after boot.

## Deploy with the hcloud CLI

```sh
hcloud firewall create --name iapstack-ssh
hcloud firewall add-rule iapstack-ssh --direction in --protocol tcp --port 22 \
  --source-ips <your-ip>/32
hcloud server create --name iapstack --type cax11 --image ubuntu-24.04 --location fsn1 \
  --ssh-key <your-key-name> --firewall iapstack-ssh --user-data-from-file cloud-init.yaml
```

## After installation

1. Connect and follow the installation until it prints `IAPStack is running.`:

   ```sh
   ssh root@<server-ip>
   tail -f /var/log/cloud-init-output.log
   ```

2. Read the bootstrap administrator key:

   ```sh
   grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= /opt/iapstack/deploy/vps/.env
   ```

3. Check `https://iapstack.example.com/readyz`, open `/dashboard/`, connect with the
   bootstrap key, and create your administrator account in the setup banner.
4. Remove the bootstrap key and apply the change:

   ```sh
   cd /opt/iapstack/deploy/vps
   sed -i 's/^IAPSTACK_BOOTSTRAP_ADMIN_KEY=.*/IAPSTACK_BOOTSTRAP_ADMIN_KEY=/' .env
   docker compose up -d --wait
   ```

5. Copy `/opt/iapstack/deploy/vps/.env` to your password manager, configure off-server
   backups, and register the store notification URLs as described in the
   [single-VM guide](../vps/README.md).

If you left the token empty, add it to `.env` as `CLOUDFLARE_TUNNEL_TOKEN=<token>` and
run `sh /opt/iapstack/deploy/vps/install.sh` to start the tunnel.

## Upgrades

Re-run the installer with the target ref. It takes a backup before changing commits,
keeps the existing `.env`, and stays on the deployed commit when `IAPSTACK_REF` is
omitted:

```sh
IAPSTACK_REF=<tag-or-full-commit-sha> sh /opt/iapstack/deploy/vps/install.sh
```

## Cleanup

Delete the server, which also deletes its disk and the database, and then the firewall:

```sh
hcloud server delete iapstack
hcloud firewall delete iapstack-ssh
```

Delete the tunnel and its DNS record in Cloudflare, and remove off-server backups you
no longer need.
