# Deploy IAPStack to a DigitalOcean Droplet

A Droplet runs the complete [single-VM stack](../vps/README.md) for less than the
[App Platform deployment](README.md): PostgreSQL, migrations, the compact API and
worker process, and a Cloudflare Tunnel connector. Paste
[`deploy/vps/cloud-init.yaml`](../vps/cloud-init.yaml) as the Droplet's user data and
it runs [`install.sh`](../vps/install.sh) on first boot.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials until your own release checks pass.

## Cost

| Size | RAM | Typical price | Notes |
| --- | --- | --- | --- |
| `s-1vcpu-1gb` | 1 GB | About USD 6/month | Low traffic. The installer adds swap and applies 1 GB tuning automatically. |
| `s-1vcpu-2gb` | 2 GB | About USD 12/month | More headroom for image builds and traffic spikes. |

The price includes the public IPv4 address and outbound transfer allowance. Weekly
Droplet backups add 20% to the Droplet price. Confirm
[live Droplet pricing](https://www.digitalocean.com/pricing/droplets) before creating
resources. App Platform remains the managed alternative at about USD 17/month.

## Before you start

1. **Cloudflare Tunnel.** Add your domain to Cloudflare (the free plan is enough).
   In Zero Trust, open **Networks → Tunnels**, create a `cloudflared` tunnel, and copy
   its token. Add a public hostname such as `iapstack.example.com` with service type
   `HTTP` and URL `iapstack:8080`.
2. **User data.** Download [`cloud-init.yaml`](../vps/cloud-init.yaml) and paste the
   token between the quotes of `CLOUDFLARE_TUNNEL_TOKEN`. Set `IAPSTACK_REF` to a
   release tag or a commit you have tested. You can leave the token empty and add it
   later over SSH.

The user data stays readable from the Droplet's metadata service by any process on
the Droplet. The installer deletes its own copy of the token file, but treat the
token as exposed to the Droplet. Rotate the token in Cloudflare if that matters to
you, or leave it empty here and add it over SSH. All other secrets are generated on
the Droplet and never appear in user data.

## Deploy with the control panel

1. Open **Networking → Firewalls → Create Firewall**. Keep one inbound rule for SSH
   limited to your own IP address and remove every other inbound rule. Keep the
   default outbound rules; the tunnel only makes outbound connections.
2. Open **Create → Droplets** and choose:
   - **Region:** any
   - **Image:** Ubuntu 24.04 (LTS) x64
   - **Size:** Basic, Regular, `s-1vcpu-1gb` or larger
   - **Authentication:** SSH key
   - **Advanced options → Add initialization scripts (free):** paste your edited
     `cloud-init.yaml`
3. Create the Droplet, then attach it to the firewall from step 1. Installation
   continues for 5–15 minutes after boot.

## Deploy with doctl

```sh
doctl compute droplet create iapstack \
  --region fra1 --image ubuntu-24-04-x64 --size s-1vcpu-1gb \
  --ssh-keys <your-key-id-or-fingerprint> \
  --user-data-file cloud-init.yaml --wait

doctl compute firewall create --name iapstack-ssh \
  --inbound-rules "protocol:tcp,ports:22,address:<your-ip>/32" \
  --outbound-rules "protocol:tcp,ports:all,address:0.0.0.0/0,address:::/0 protocol:udp,ports:all,address:0.0.0.0/0,address:::/0 protocol:icmp,address:0.0.0.0/0,address:::/0" \
  --droplet-ids "$(doctl compute droplet get iapstack --format ID --no-header)"
```

## After installation

1. Connect and follow the installation until it prints `IAPStack is running.`:

   ```sh
   ssh root@<droplet-ip>
   tail -f /var/log/cloud-init-output.log
   ```

2. Read the bootstrap administrator key:

   ```sh
   grep ^IAPSTACK_BOOTSTRAP_ADMIN_KEY= /opt/iapstack/deploy/vps/.env
   ```

3. Check `https://iapstack.example.com/readyz`, open `/dashboard/`, connect with the
   bootstrap key, and create a durable administrator key.
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
IAPSTACK_REF=<tag-or-commit> sh /opt/iapstack/deploy/vps/install.sh
```

## Cleanup

Deleting the Droplet also deletes its disk and the database:

```sh
doctl compute droplet delete iapstack
doctl compute firewall delete "$(doctl compute firewall list --format ID,Name --no-header | awk '$2 == "iapstack-ssh" {print $1}')"
```

Delete the tunnel and its DNS record in Cloudflare, and remove off-server backups you
no longer need.
