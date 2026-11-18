# Railway deployment

IAPStack uses Railway's project-level Infrastructure as Code (IaC) replacement for
the deprecated per-service `railway.toml` and `railway.json` format. The
[`railway.ts`](../../.railway/railway.ts) graph defines:

- one managed PostgreSQL database on Railway's private network;
- one `IAPStack API` service with a readiness check and pre-deploy migration, which
  also runs the durable worker in the default compact topology;
- optionally, one private `IAPStack Worker` service in the split topology; and
- generated sealed protection and metrics secrets.

## Topology and cost

Railway bills the CPU and memory that services actually use: about USD 10 per GB of
RAM and USD 20 per vCPU each month, plus USD 0.15 per GB-month of volume storage. The
[Hobby plan](https://docs.railway.com/pricing/plans) costs USD 5 per month and includes
USD 5 of that usage. New accounts get a 30-day trial with a one-time credit; the Free
plan that follows has too little monthly credit for an always-on stack.

Measured idle on a local Docker host, the compact `server` process used about 20 MiB
of RAM and under 1% of one CPU, and PostgreSQL about 55 MiB and 1% of one CPU. At
Railway's rates that is roughly USD 1–2 per month, so a small compact installation
normally stays within the Hobby plan's included usage, for USD 5 per month in total.
Traffic, Railway's own PostgreSQL image settings, and image builds change these
numbers, so set a usage limit and check the usage page after the first days.

The topology is chosen by one constant at the top of
[`railway.ts`](../../.railway/railway.ts):

| `topology` | Services | Use when |
| --- | --- | --- |
| `"compact"` (default) | PostgreSQL and `IAPStack API` running `iapstack server` (API and worker) | Lowest cost; most installations |
| `"split"` | PostgreSQL, `IAPStack API` running `iapstack api`, and `IAPStack Worker` | API and worker need independent sizing or restarts |

Both topologies keep the API service's name. Renaming it would make Railway recreate
the service and regenerate its sealed protection keys, which makes stored store
credentials and webhook secrets unreadable.

Switching an existing project from split to compact changes the API start command
and deletes `IAPStack Worker`. `railway config apply` asks you to confirm the
destructive change. PostgreSQL, the API's variables, and its domain are kept. Switching
back recreates the worker, which references the API's existing keys.

Keep Railway's **Serverless** (app sleeping) setting off for the IAPStack services.
A sleeping service stops the durable worker until the next HTTP request.

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

Review the plan carefully before applying it. With the default compact topology, the
first apply creates `IAPStack API` and `IAPStack Postgres` in `europe-west4`; the split
topology also creates `IAPStack Worker`. The API's pre-deploy command applies the
schema before the API goes live. A split worker has an always-restart policy, so an
initial start that races the first migration recovers automatically.

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
2. Confirm that only the API receives an HTTP domain and that the database (and the
   worker, in the split topology) has no public networking. Publish the compact
   topology as the default template so new installations start within the Hobby
   plan's included usage.
3. Keep each root-key generator as
   `secret(64, "abcdef0123456789")`; keep the bootstrap generator alphanumeric with
   length 48.
4. Confirm the API references the database's private `DATABASE_URL`. In the split
   topology, also confirm the worker variables reference the corresponding API
   variables and the same database URL.
5. Create the template, copy its deployment URL, and add that URL to the main README.

Until that account-owned template URL exists, the checked-in IaC file is the
reproducible setup path; there is deliberately no fake or unstable Deploy button.
