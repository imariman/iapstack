# Render deployment

IAPStack has three Render profiles that use the same image and PostgreSQL schema:

| Profile | Blueprint | Runtime shape | Intended use |
| --- | --- | --- | --- |
| Compact sandbox | [`render.yaml`](../../render.yaml) | One free `server` web service + one free signed-webhook receiver + free PostgreSQL | Disposable store sandbox testing |
| Compact production | [`render.compact.yaml`](render.compact.yaml) | One paid `server` web service + paid PostgreSQL | Default live topology |
| Split production | [`render.split.yaml`](render.split.yaml) | Paid API + paid worker + paid PostgreSQL | Independent capacity or fault isolation |

The default compact sandbox runs API and worker lifecycles in one Go process with
worker concurrency `1`. A second free service runs an isolated signed-webhook receiver
that retains only event IDs, application IDs, event types, timestamps, and SHA-256 body
fingerprints in PostgreSQL. It does not store raw webhook bodies. Render Free cannot
run a pre-deploy command, so this exact single-instance profile enables startup
migrations. Production profiles disable startup migrations and use Render's paid
pre-deploy migration command.

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
3. Generate a dedicated 32-byte-or-longer value for
   `IAPSTACK_WEBHOOK_RECEIVER_SECRET` and enter it when Render prompts. Keep it outside
   source control; the same value is entered once in the IAPStack application webhook
   form after deployment.
4. Confirm the Blueprint contains two free web services and one free PostgreSQL
   database, then approve it.
5. Wait until both services report `Live` and each `GET /readyz` returns HTTP 204 or 200.
6. Reveal and copy `IAPSTACK_BOOTSTRAP_ADMIN_KEY`, open
   `https://<your-service>.onrender.com/dashboard/`, create a stored administrator key,
   and verify it.
7. Open the application management workspace and configure its webhook URL as
   `https://<your-receiver>.onrender.com/webhooks/iapstack`, using the exact receiver
   secret from step 3 as the signing secret.
8. Remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY` and manually deploy once. Keep the generated
   `IAPSTACK_METRICS_BEARER_TOKEN`; it protects `/metrics` independently of administrator
   and application credentials.
9. Import the repository Grafana dashboard and confirm the `sandbox` environment
   reports queue metrics after approximately two minutes.

The receiver verifies `v1` HMAC-SHA-256 over the exact timestamp and raw body, rejects
deliveries outside a five-minute window, and deduplicates event IDs durably. A sleeping
free receiver may exceed the first ten-second delivery attempt while it wakes; the
IAPStack worker retries the durable outbox message. The receiver is a sandbox fixture,
not a replacement for the application's production backend.

Render currently grants 750 Free instance hours per workspace each month. Both free
web services draw from that shared allowance only while running; spun-down services do
not. This two-service profile is therefore suitable for intermittent sandbox testing,
not for keeping both processes continuously awake at no cost.

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
