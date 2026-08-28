# Railway deployment

IAPStack uses Railway's project-level Infrastructure as Code (IaC) replacement for
the deprecated per-service `railway.toml` and `railway.json` format. The
[`railway.ts`](../../.railway/railway.ts) graph defines:

- one managed PostgreSQL database on Railway's private network;
- one API service with a readiness check and pre-deploy migration;
- one private background worker; and
- generated sealed secrets that are referenced by both processes.

Railway offers a new-account trial for up to 30 days with a one-time credit. It then
falls back to the Free plan's small monthly credit. IAPStack's continuously running
API, worker, and PostgreSQL database are usage-billed and are not guaranteed to fit
inside those credits. Configure a spend limit before testing.

## Apply the infrastructure

Requirements are Node.js 22 or newer, Railway CLI 5.42.1 or newer, and a Railway
account connected to GitHub.

```sh
npm install --prefix .railway
railway login
railway init
railway config plan
railway config apply
```

Review the plan carefully before applying it. The first apply creates `IAPStack API`,
`IAPStack Worker`, and `IAPStack Postgres` in `europe-west4`. The API's pre-deploy
command applies the schema before the API goes live. The worker has an always-restart
policy, so an initial start that races the first migration recovers automatically.

The file targets the upstream `main` branch. Change the `github(...)` branch in
`.railway/railway.ts` when testing an unmerged branch or a fork.

## Enable HTTPS

Railway intentionally does not persist generated service domains in IaC. Open
`IAPStack API`, select **Settings → Networking → Public Networking**, and click
**Generate Domain**. Railway provisions and renews TLS automatically. Do not create a
public domain or TCP proxy for the worker or PostgreSQL.

Verify `https://<generated-domain>/readyz` returns HTTP 200 before configuring a mobile
client or store notification URL.

## Complete the secure bootstrap

1. Reveal `IAPSTACK_BOOTSTRAP_ADMIN_KEY` only from the API service's Variables page.
2. Use it in the IAPStack dashboard to create a stored administrator key and save the
   returned bearer.
3. Remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY` from the API service and redeploy it.
4. Remove the same variable declaration from your fork of `.railway/railway.ts` before
   the next IaC apply so the installation credential is not recreated.

The encryption and fingerprint roots are generated as sealed 64-character hex values.
The worker references the API variables instead of generating its own keys. Never
replace the fingerprint root during ordinary encryption rotation.

## Publish the one-click template

Publishing requires a Railway account because template IDs belong to a Railway
workspace. After the IaC deployment is healthy:

1. Open the project settings and choose **Generate Template from Project**.
2. Confirm that only the API receives an HTTP domain and that the worker/database have
   no public networking.
3. Keep each root-key generator as
   `secret(64, "abcdef0123456789")`; keep the bootstrap generator alphanumeric with
   length 48.
4. Confirm the worker variables reference the corresponding API variables and both
   services reference the database's private `DATABASE_URL`.
5. Create the template, copy its deployment URL, and add that URL to the main README.

Until that account-owned template URL exists, the checked-in IaC file is the
reproducible setup path; there is deliberately no fake or unstable Deploy button.
