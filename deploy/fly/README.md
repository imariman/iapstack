# Deploy IAPStack to Fly.io

[`fly.toml`](../../fly.toml) builds the repository Dockerfile and creates separate
API and worker Machines from one image. Fly Proxy exposes only the API over HTTPS,
the worker stays private, and a temporary release Machine applies migrations before
each release.

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use. Use an
> isolated app and provider-sandbox credentials.

## Cost and account requirements

A Fly.io account can be opened without an immediate charge, but there is no permanent
free runtime. The current trial ends after two total Machine-hours or seven days,
whichever comes first, and trial Machines automatically stop after five minutes.
That is useful for checking a container boot, not for a complete worker lifecycle.

Fly Machines are usage-priced by region. This manifest creates two always-on
`shared-cpu-1x` Machines with 512 MiB RAM. Fly Managed Postgres Basic currently costs
USD 38 per month plus USD 0.28 per provisioned GB; its default 10 GB brings the
database baseline to USD 40.80 before the two Machines and network usage. Confirm the
live estimate first. For this topology, Fly.io is not the economical sandbox option.

## Deploy

1. Install `flyctl`, sign in, and choose a globally unique app name:

   ```sh
   fly auth login
   fly apps create <unique-iapstack-app>
   ```

2. Create Fly Managed Postgres in the same region. Record the cluster ID returned by
   `fly mpg list`, then attach it using IAPStack's expected variable name:

   ```sh
   fly mpg create --name <unique-iapstack-db> --region fra --plan basic --volume-size 10
   fly mpg attach <cluster-id> --app <unique-iapstack-app> --variable-name IAPSTACK_DATABASE_URL
   ```

3. Generate and store the runtime secrets. Each command substitution generates a
   different value; the values themselves are not added to the repository:

   ```sh
   fly secrets set --app <unique-iapstack-app> \
     IAPSTACK_PROTECTION_KEY="$(openssl rand -base64 32)" \
     IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32)" \
     IAPSTACK_METRICS_BEARER_TOKEN="$(openssl rand -base64 32)" \
     IAPSTACK_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base64 32)"
   ```

4. Deploy from the repository. `--app` deliberately overrides the placeholder app
   name committed in `fly.toml`:

   ```sh
   fly deploy --app <unique-iapstack-app>
   fly scale count api=1 worker=1 --app <unique-iapstack-app>
   fly status --app <unique-iapstack-app>
   ```

5. Verify `https://<unique-iapstack-app>.fly.dev/readyz`, open `/dashboard/`, create
   the first durable administrator key, and remove the bootstrap secret:

   ```sh
   fly secrets unset IAPSTACK_BOOTSTRAP_ADMIN_KEY --app <unique-iapstack-app>
   ```

The API Machine is intentionally configured not to auto-stop because store
notifications must have a stable endpoint. The worker has an `always` restart policy
and no public service.

## Cleanup

Deleting the app does not delete Managed Postgres. Remove both resources and verify
them independently in the dashboard so billing stops:

```sh
fly apps destroy <unique-iapstack-app>
fly mpg destroy <cluster-id>
```

Official references:

- [Fly process groups](https://fly.io/docs/launch/processes/)
- [`fly.toml` reference](https://fly.io/docs/reference/configuration/)
- [Managed Postgres](https://fly.io/docs/mpg/)
- [Fly.io free trial](https://fly.io/docs/about/free-trial/)
- [Fly.io pricing](https://fly.io/docs/about/pricing/)
