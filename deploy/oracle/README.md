# Deploy IAPStack to Oracle Cloud Always Free

[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)

The button opens an [Oracle Resource Manager](https://docs.oracle.com/en-us/iaas/Content/ResourceManager/Concepts/landing.htm)
stack built from this directory. It creates:

- a VCN, internet gateway, route table, and subnet
- a security list that allows outbound traffic only, plus SSH from one CIDR when you
  provide it
- one `VM.Standard.A1.Flex` Ubuntu 24.04 VM (2 OCPUs, 12 GB RAM, and a 50 GB boot
  volume by default)

On first boot, cloud-init runs [`deploy/vps/install.sh`](../vps/install.sh), which
installs Docker and starts the [single-VM stack](../vps/README.md): PostgreSQL,
migrations, the compact API and worker process, and the Cloudflare Tunnel connector.
The stack's resources stay within Oracle's
[Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm)
limits as long as the tenancy has no other A1 instances or block volumes that use up
those limits.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials until your own release checks pass.

## Before you click

1. **Oracle Cloud account.** Signup requires a card for identity verification.
   Always Free A1 instances are available only in your home region. Upgrading the
   account to Pay As You Go prevents Oracle from reclaiming idle Always Free
   instances, and you are still not charged while you stay within Always Free limits.
   Set a budget alert after upgrading.
2. **Cloudflare Tunnel.** Add your domain to Cloudflare (the free plan is enough).
   In Zero Trust, open **Networks → Tunnels**, create a `cloudflared` tunnel, and
   copy its token. Add a public hostname such as `iapstack.example.com` with service
   type `HTTP` and URL `iapstack:8080`. You can also leave the token empty and add it
   later.

## Deploy

1. Click **Deploy to Oracle Cloud** and sign in. Accept the terms and choose the
   compartment and availability domain.
2. Paste the tunnel token and hostname. Optionally add an SSH public key and your own
   IP as `x.x.x.x/32` so you can reach the VM.
3. Leave **Run apply** checked and create the stack. The apply job takes 2–3 minutes.
   The installation then continues on the VM for 5–10 minutes while the image builds.
4. Open the job's **Outputs** and copy `bootstrap_admin_key`.
5. Check `https://iapstack.example.com/readyz`, then open `/dashboard/`. Connect with
   the bootstrap key and create your administrator account in the setup banner.
6. Remove the bootstrap key from the VM:

   ```sh
   cd /opt/iapstack/deploy/vps
   sudo sed -i 's/^IAPSTACK_BOOTSTRAP_ADMIN_KEY=.*/IAPSTACK_BOOTSTRAP_ADMIN_KEY=/' .env
   sudo docker compose up -d --wait
   ```

7. Copy `/opt/iapstack/deploy/vps/.env` to your password manager, configure
   off-server backups, and register the store notification URLs as described in the
   [single-VM guide](../vps/README.md).

Without SSH, you can run the commands in steps 6 and 7 from the instance's
**Cloud Shell connection** or **Console connection** in the OCI Console.

## Secrets and state

- The tunnel token and bootstrap key reach the VM through instance metadata
  (`user_data`). Users who can read the instance in OCI can see them, and processes on
  the VM can read them from the metadata service. The installer writes them to a
  root-only file and deletes it after installation. Rotate the tunnel token in
  Cloudflare if that exposure matters to you; the bootstrap key stops working once
  you remove it.
- The bootstrap key is also stored in the stack's Terraform state. Restrict access to
  the stack and its jobs.
- All other secrets, including the database password and protection keys, are
  generated on the VM by `init-env.sh` and never leave it.

## Upgrades

The stack only creates infrastructure; it does not track IAPStack releases. To
upgrade, connect to the VM and re-run the installer with the target ref. It takes a
backup before changing commits and keeps the existing `.env`. Without
`IAPSTACK_REF`, it stays on the deployed commit:

```sh
sudo IAPSTACK_REF=<tag-or-full-commit-sha> sh /opt/iapstack/deploy/vps/install.sh
```

Changing the stack's variables and applying again does not change the running VM.
The image and user data are ignored after creation so that a newer Ubuntu image never
replaces the database.

## Troubleshooting

- **"Out of host capacity"**: A1 capacity is limited in popular regions. Destroy the
  failed stack, then retry later or with another availability domain.
- **Limit or quota errors**: another A1 instance or boot volume already uses the
  tenancy's Always Free limits. Reduce `instance_ocpus` or `instance_memory_gbs`, or
  remove the other resources.
- **Installation progress**: `sudo tail -f /var/log/cloud-init-output.log` on the VM.
  Re-run `sudo sh /opt/iapstack/deploy/vps/install.sh` after fixing a failure.

## Cleanup

In Resource Manager, run **Destroy** on the stack. This deletes the VM, its boot
volume (including the database), and the network. Delete the tunnel and its DNS
record in Cloudflare, and remove off-server backups you no longer need.
