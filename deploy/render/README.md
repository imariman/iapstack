# Render deployment

The root [`render.yaml`](../../render.yaml) is a Render Blueprint for the complete
IAPStack runtime:

- one public API service with Render-managed HTTPS;
- one private background worker;
- one private Render Postgres database; and
- an API pre-deploy migration command.

The Blueprint uses paid minimum-size compute for all three durable components. Render
does not offer a free background-worker plan, and the free PostgreSQL plan expires after
30 days. The paid database default prevents a one-click deployment from silently
becoming disposable. Review Render's current estimate before approving the Blueprint.

## Deploy

1. Click **Deploy to Render** from the repository README and sign in or create a Render
   account.
2. Review the three resources and the monthly estimate, then approve the Blueprint.
3. Wait until `iapstack-api` reports `Live` and `GET /readyz` returns HTTP 200. The
   first worker start can briefly retry while the API pre-deploy migration completes.
4. In the API service's Environment page, reveal and copy
   `IAPSTACK_BOOTSTRAP_ADMIN_KEY`. Treat it as a secret.
5. Open `https://<your-service>.onrender.com/dashboard/`, use the bootstrap key to
   create a stored administrator key, save the returned bearer, and verify it works.
6. Remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY` from the API service and manually deploy the
   API once. The installation bearer should not remain in normal runtime configuration.

Render generates independent Base64-encoded 256-bit encryption and fingerprint keys
on first creation. Do not regenerate either secret during ordinary redeploys. Before
rotating encryption, convert `IAPSTACK_PROTECTION_KEY` to the rotation-capable
`IAPSTACK_PROTECTION_KEYS` JSON keyring described in the main operations guide.

Automatic deploys are disabled because a public one-click template must not redeploy
every installation whenever the upstream repository changes. Upgrade deliberately by
syncing the Blueprint and triggering the API and worker deploys after reviewing release
notes and taking a database backup.

## Disposable sandbox variation

For a short-lived test, fork the repository and change only the database `plan` in
`render.yaml` from `0.1c-256mb` to `free`. Free Render Postgres expires after 30 days,
is limited to one instance per workspace, and is unsuitable for production data. Keep
the API and worker on paid plans: migrations require the API's paid pre-deploy command,
and background workers have no free plan.

## Network boundary

Only the API is public. The database has an empty public IP allowlist, and the worker
has no public service endpoint. Render terminates TLS for the API's `onrender.com`
hostname. Do not expose the worker probe port or database separately.
