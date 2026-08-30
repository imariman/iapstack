# Grafana Cloud monitoring

IAPStack sends bounded application and worker metrics directly to Grafana Cloud over
OTLP/HTTPS. Grafana Cloud is managed, so this profile does not deploy Grafana,
Prometheus, Alloy, or another Render service. Outbound delivery also means a sleeping
free Render service is not awakened by an external metrics scrape.

## Administrator-only access

Treat Grafana as a separate administrator control plane:

1. Keep anonymous access, externally shared dashboards, public dashboard links, and
   snapshots disabled.
2. Invite only the people who should administer IAPStack and assign them the Grafana
   organization `Admin` role. Do not create Viewer or Editor memberships for the
   IAPStack stack when the requirement is administrator-only access.
3. Require MFA on the Grafana account. The IAPStack administrator bearer does not log
   a user into Grafana; the two authentication boundaries remain independent.
4. Create one Cloud Access Policy token with metrics-write/OTLP ingestion scope only.
   Do not grant that machine token dashboard-read, user-management, or administrator
   scopes.

Knowing the Grafana stack URL is not authorization. Grafana Cloud login and the Admin
role enforce page access even if the URL is disclosed.

## Connect Render

Open the Grafana Cloud stack's OpenTelemetry card and copy its OTLP endpoint, instance
ID, and a write-only access-policy token. Supply them during initial Render Blueprint
creation:

- `IAPSTACK_GRAFANA_OTLP_ENDPOINT`
- `IAPSTACK_GRAFANA_OTLP_USERNAME`
- `IAPSTACK_GRAFANA_OTLP_TOKEN`

The endpoint must be HTTPS. IAPStack appends `/v1/metrics` when the copied URL is the
Grafana OTLP base URL, batches one export per minute, uses gzip, and never logs the
credentials. All three values are optional together; leaving all three empty disables
cloud export without affecting local metrics.

The independent `IAPSTACK_METRICS_BEARER_TOKEN` protects the Prometheus-compatible
`/metrics` endpoint. Render generates this token. If the token is absent, the endpoint
returns 404; an invalid bearer returns 401. Direct OTLP export does not use or expose
this bearer.

## Dashboard and alerts

1. In Grafana, import [`iapstack-dashboard.json`](iapstack-dashboard.json) and select
   the managed Prometheus data source.
2. Confirm the `sandbox` environment appears and all three queue families report
   within approximately two export intervals.
3. Load [`alerts.yaml`](alerts.yaml) through Grafana Cloud's Prometheus rules workflow,
   then connect the rules to an administrator-only notification contact point.
4. Keep the production rules filtered to `deployment_environment_name="production"`.
   The free sandbox intentionally sleeps and therefore must not page an administrator.

The separation signal is sustained runnable-job age combined with Render CPU, memory,
or API latency pressure. A terminal queue failure is an incident, but does not by
itself prove that the worker needs a separate service.
