# Kubernetes

Plain manifests for one single-replica IAPStack installation. They package the
container that already ships. They are not a Helm chart, a controller, a custom
resource, or a cloud module for AWS, Google Cloud, or Azure.

The namespace `iapstack` is an operator convenience. It is not a tenant boundary.
PostgreSQL 18 and a TLS ingress controller are assumed to exist already. These
files do not install PostgreSQL, a database operator, or a StatefulSet, and they
do not automate backups. Backup and restore stay the procedure in
[`docs/operations.md`](../../docs/operations.md).

Do not apply this directory in one shot. `kubectl apply -f deploy/k8s` does not
wait for migration, and it can create Deployments before the namespace or the Job.

## Image

The Job and both Deployments use the placeholder
`ghcr.io/imariman/iapstack:VERSION`. Replace `VERSION` on all three with one
immutable digest before apply:

```text
ghcr.io/imariman/iapstack@sha256:<release-digest>
```

The migrate Job must use that same digest. Verify provenance as described in
[`docs/operations.md`](../../docs/operations.md) before deploying it. Do not deploy
the `VERSION` tag.

Kubernetes `command` replaces the image entrypoint. Each workload sets `command`
to `/usr/local/bin/iapstack` and `args` to one mode: `migrate`, `api`, or `worker`.
That is the same process Compose and Fly start. Compact `server` mode is not the
Kubernetes shape. `IAPSTACK_AUTO_MIGRATE` is not set.

## Objects

| File | Object | Role |
| --- | --- | --- |
| `namespace.yaml` | Namespace `iapstack` | Convenience only |
| `job-migrate.yaml` | Job `iapstack-migrate` | `migrate`, then exit. `completions: 1`, `parallelism: 1`, `backoffLimit: 3`, `restartPolicy: Never` |
| `deployment-api.yaml` | Deployment `iapstack-api` | `api`, one replica, port 8080 |
| `deployment-worker.yaml` | Deployment `iapstack-worker` | `worker`, one replica, port 8081, no Ingress |
| `service-api.yaml` | Service `iapstack-api` | ClusterIP 8080. The only Ingress backend |
| `service-api-metrics.yaml` | Service `iapstack-api-metrics` | ClusterIP 8080 to the API pods. Not an Ingress backend |
| `service-worker.yaml` | Service `iapstack-worker` | ClusterIP 8081. Not an Ingress backend |
| `ingress-api.yaml` | Ingress `iapstack-api` | TLS for one API host |

The API and the worker listen on different ports, so metrics are two Services.
One shared Service would invent a port the process does not have. Kubelet probes
the pods directly: startup and liveness use `GET /healthz`, readiness uses
`GET /readyz`. A pod whose schema does not match the binary stays unready.

## Secrets

Runtime values come from the Kubernetes Secret `iapstack-secrets`. Do not commit
the Secret, and do not put values in a ConfigMap or in these manifests. Generate
the keys with the procedure in [`docs/operations.md`](../../docs/operations.md).
Placeholders below are not credentials.

| Key | Who receives it |
| --- | --- |
| `IAPSTACK_DATABASE_URL` | migrate Job, API, worker |
| `IAPSTACK_PROTECTION_ACTIVE_KEY_ID` | migrate Job, API, worker |
| `IAPSTACK_PROTECTION_KEYS` | migrate Job, API, worker |
| `IAPSTACK_PROTECTION_FINGERPRINT_KEY` | migrate Job, API, worker |
| `IAPSTACK_METRICS_BEARER_TOKEN` | migrate Job, API, worker |
| `postgres-ca.crt` | mounted file, not an environment variable |
| `IAPSTACK_BOOTSTRAP_ADMIN_KEY` | API only, optional |

Use `IAPSTACK_PROTECTION_KEYS` (the JSON object). Do not also set
`IAPSTACK_PROTECTION_KEY`. The metrics bearer must be at least 32 characters.
The bootstrap admin key is API-only and only until a durable admin key exists.
The reference is `optional: true`, so the API still starts after the key is
removed. Recreate the Secret without that key, then restart `iapstack-api`.
The worker and the migrate Job do not receive it.

The Ingress certificate is a different Secret, `iapstack-api-tls`, for the API
host. These manifests do not request a certificate and do not name an issuer.

## PostgreSQL TLS

Copying Compose `sslmode=disable` into the cluster is wrong once database
traffic leaves one trusted host. `IAPSTACK_DATABASE_URL` must use certificate
verification and the mounted bundle:

```text
host=<certificate-name> port=5432 dbname=iapstack user=iapstack password=REPLACE sslmode=verify-full sslrootcert=/etc/iapstack/postgres/ca.crt
```

Prefer this keyword form so a password can contain URL-reserved characters.
`sslmode=verify-full` checks the server name, so the host must be a name on the
certificate, not an address that the certificate does not list. Mount the PEM
that signed the server certificate as Secret key `postgres-ca.crt`. The
workloads mount it at `/etc/iapstack/postgres/ca.crt` (mode `0440`, group
`65532`). There is no PostgreSQL Service in this set. The operator supplies the
endpoint. These manifests do not choose `pool_max_conns`.

## Private networks and exposure

`IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS=false` and
`IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS=false` are set on the migrate Job, the
API, and the worker. Do not turn either on so that a webhook can reach a
ClusterIP. If an approved destination is inside the cluster, set the matching
flag on both the API and the worker and allow only that destination with
egress policy. That exception is the documented residual risk, not a Kubernetes
default.

The Ingress publishes only Service `iapstack-api`. Its paths are `/v1` and
`/dashboard`. It does not publish `/metrics`, and it has no default backend.
`/healthz` and `/readyz` are omitted because no ingress class is assumed to
need them; kubelet already probes the pods. Replace `api.example.invalid` in
both the `tls` and `rules` hosts. No `ingressClassName` is set. Uncomment that
field only when this cluster has no default IngressClass. Do not treat any
controller as required. Disable plain HTTP on that controller if it would
serve the same paths without TLS; there is no portable annotation for that.

Scrape `/metrics` from inside the cluster over HTTP with the dedicated bearer.
TLS stops at the Ingress; these Services are not TLS. The bearer stays
mandatory even on the private Services.

- API: `http://iapstack-api-metrics.iapstack.svc.cluster.local:8080/metrics`
- Worker: `http://iapstack-worker.iapstack.svc.cluster.local:8081/metrics`

Do not point Ingress at `iapstack-api-metrics` or `iapstack-worker`. The public
Service `iapstack-api` is the same API port, so an Ingress path of `/` or
`/metrics` would publish metrics. Do not add either path.

PostgreSQL has no Service here. Egress from the API and the worker should be
limited to PostgreSQL, the store APIs, and the configured webhook hosts. Those
destinations are operator-specific, so this directory does not ship a
NetworkPolicy.

## Single replica

This is a single-replica install: one API pod and one worker pod. Worker HPA
is out of scope. Do not autoscale the worker and do not add leader election for
it. A second worker is not forbidden by the process, but this package does not
scale it.

An API HorizontalPodAutoscaler is not included. Adding one later requires CPU
requests, an explicit `pool_max_conns` on `IAPSTACK_DATABASE_URL`, and
readiness on `/readyz`. The same protection keys, fingerprint key, and metrics
bearer must be on every API pod. Use `maxSurge: 1` when leaving a single
replica. The migrate Job stays `completions: 1` and is not a scale target.
None of those scaler settings are implied by the Deployment that exists today.
No CPU or memory requests are set, because no capacity measurement and no HPA
exist yet.

## Apply

1. Replace the image placeholder with one digest on the Job and both Deployments.
   Replace the Ingress host, and create `iapstack-api-tls` for that host.
2. Apply the namespace and create `iapstack-secrets` (example only; replace every
   `REPLACE` value, and do not commit the result):

   ```sh
   kubectl apply -f deploy/k8s/namespace.yaml
   kubectl create secret generic iapstack-secrets \
     --namespace iapstack \
     --from-file=postgres-ca.crt=./postgres-ca.crt \
     --from-literal=IAPSTACK_DATABASE_URL='host=REPLACE port=5432 dbname=REPLACE user=REPLACE password=REPLACE sslmode=verify-full sslrootcert=/etc/iapstack/postgres/ca.crt' \
     --from-literal=IAPSTACK_PROTECTION_ACTIVE_KEY_ID='REPLACE' \
     --from-literal=IAPSTACK_PROTECTION_KEYS='REPLACE' \
     --from-literal=IAPSTACK_PROTECTION_FINGERPRINT_KEY='REPLACE' \
     --from-literal=IAPSTACK_METRICS_BEARER_TOKEN='REPLACE' \
     --from-literal=IAPSTACK_BOOTSTRAP_ADMIN_KEY='REPLACE'
   ```

3. Migrate, and wait until the Job completes, before starting either Deployment:

   ```sh
   kubectl apply -f deploy/k8s/job-migrate.yaml
   kubectl wait --namespace iapstack --for=condition=complete job/iapstack-migrate --timeout=300s
   ```

4. Apply the Services, Deployments, and Ingress:

   ```sh
   kubectl apply \
     -f deploy/k8s/service-api.yaml \
     -f deploy/k8s/service-api-metrics.yaml \
     -f deploy/k8s/service-worker.yaml \
     -f deploy/k8s/deployment-api.yaml \
     -f deploy/k8s/deployment-worker.yaml \
     -f deploy/k8s/ingress-api.yaml
   kubectl rollout status --namespace iapstack deployment/iapstack-api
   kubectl rollout status --namespace iapstack deployment/iapstack-worker
   ```

A failed Job stays failed after `backoffLimit`. Delete it before trying again.
Readiness is not a reason to race the Job against new pods.

## Upgrade

Follow [`docs/operations.md`](../../docs/operations.md): back up, record the
current digest, run `migrate` for the new digest, roll API processes, then roll
workers. The Job pod template is immutable and a completed Job does not run
again. Delete it, then apply the Job with the new digest and wait for
`Complete` before rolling the Deployments:

```sh
kubectl delete job --namespace iapstack iapstack-migrate --wait=true
kubectl apply -f deploy/k8s/job-migrate.yaml
kubectl wait --namespace iapstack --for=condition=complete job/iapstack-migrate --timeout=300s
```

For a schema rollback, stop the API and the workers before migrating down, as
that operations guide describes. The `migrate` mode in this Job applies the
embedded migrations forward; it is not a scale target.
