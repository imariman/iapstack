# Render deployment

IAPStack has three Render profiles that use the same image and PostgreSQL schema:

| Profile | Blueprint | Runtime shape | Intended use |
| --- | --- | --- | --- |
| Compact sandbox | [`render.yaml`](../../render.yaml) | One free `server` web service + free PostgreSQL | Disposable store sandbox testing |
| Compact production | [`render.compact.yaml`](render.compact.yaml) | One paid `server` web service + paid PostgreSQL | Default live topology |
| Split production | [`render.split.yaml`](render.split.yaml) | Paid API + paid worker + paid PostgreSQL | Independent capacity or fault isolation |

The default compact sandbox runs API and worker lifecycles in one Go process with
worker concurrency `1`. Render Free cannot run a pre-deploy command, so this exact
single-instance profile enables startup migrations. Production profiles disable
startup migrations and use Render's paid pre-deploy migration command.

## Grafana first

Create the free Grafana Cloud stack before the initial Render Blueprint flow. Follow
the [administrator-only Grafana guide](../grafana/README.md) and collect the HTTPS OTLP
endpoint, instance ID, and metrics-write-only access-policy token. Render prompts for
these three values because the Blueprint marks them `sync: false`. The split profile
prompts once per service; enter the same three values for both API and worker.

IAPStack sends metrics outward while it is running. Grafana therefore does not scrape
the Render URL every minute and cannot prevent the free service from sleeping. No
Grafana, Prometheus, or Alloy service is deployed on Render.

## Deploy the sandbox

1. Click **Deploy to Render** from the repository README and sign in or create an
   account.
2. Enter `IAPSTACK_GRAFANA_OTLP_ENDPOINT`, `IAPSTACK_GRAFANA_OTLP_USERNAME`, and
   `IAPSTACK_GRAFANA_OTLP_TOKEN` from the Grafana Cloud OpenTelemetry card.
3. Confirm the Blueprint contains one free web service and one free PostgreSQL database,
   then approve it.
4. Wait until `iapstack-sandbox` reports `Live` and `GET /readyz` returns HTTP 200.
5. Reveal and copy `IAPSTACK_BOOTSTRAP_ADMIN_KEY`, open
   `https://<your-service>.onrender.com/dashboard/`, create a stored administrator key,
   and verify it.
6. Remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY` and manually deploy once. Keep the generated
   `IAPSTACK_METRICS_BEARER_TOKEN`; it protects `/metrics` independently of administrator
   and application credentials.
7. Import the repository Grafana dashboard and confirm the `sandbox` environment
   reports queue metrics after approximately two minutes.

Free Render PostgreSQL expires after 30 days and is unsuitable for production data.
The service can sleep after inactivity; durable work already accepted into PostgreSQL
resumes when the process wakes. Automatic deploys remain disabled.

## Move to production

Use `render.compact.yaml` first. It keeps one paid application service while retaining
the ready-to-use `api` and `worker` modes. Move to `render.split.yaml` only when
sustained queue-age, API-latency, CPU, memory, restart, or isolation evidence justifies
the second service. Splitting uses the same database and image; no data migration is
required beyond the normal release migration.

Render generates independent Base64-encoded 256-bit protection, fingerprint, and
metrics bearer secrets. Do not regenerate them during ordinary redeploys. Convert the
single protection key to `IAPSTACK_PROTECTION_KEYS` before encryption-key rotation.

## Access boundaries

Render exposes only the web service over managed HTTPS. PostgreSQL has an empty public
IP allowlist, and the split worker has no public endpoint. `/metrics` is disabled when
its dedicated bearer is missing and returns 401 for an invalid bearer. Grafana Cloud
page access is separate: keep public/anonymous sharing disabled and grant only intended
IAPStack operators the Grafana Admin role.
