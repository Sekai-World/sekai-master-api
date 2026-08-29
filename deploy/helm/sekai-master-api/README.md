# sekai-master-api Helm chart

This chart deploys one application image in two runtime roles:

- `serve`: horizontally scalable public read/query API.
- `control`: single-replica admin, webhook, migration, and master-data sync workload.

It targets the official F5 NGINX Ingress Controller using standard
`networking.k8s.io/v1` Ingress resources. It does not install PostgreSQL, Redis,
an ingress controller, or TLS certificates.

## Prerequisites

- Kubernetes 1.24 or newer.
- Helm 3 or 4.
- External PostgreSQL and persistent or managed Redis.
- An installed F5 NGINX Ingress Controller and matching `IngressClass` (the
  default class name is `nginx`).
- A published application image containing `/usr/local/bin/sekai-master-api`.

## Required configuration

Create Secrets and, optionally, ConfigMaps outside this chart. The chart never
renders a Secret or accepts secret values in its defaults.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: sekai-master-api-runtime
type: Opaque
stringData:
  DATABASE_URL: postgres://...
  REDIS_PASSWORD: ...
---
apiVersion: v1
kind: Secret
metadata:
  name: sekai-master-api-control
type: Opaque
stringData:
  MASTER_DATA_GITHUB_TOKEN: ...
  MASTER_DATA_GITHUB_WEBHOOK_SECRET: ...
  OIDC_ISSUER_URL: https://identity.example.com/realms/sekai
  OIDC_AUDIENCE: sekai-api
  OIDC_CLIENT_ID: sekai-api
  OIDC_REDIRECT_URL: https://master-api-admin.example.com/api/v1/admin/login/callback
```

Only include keys required by your configuration. Keep non-secret settings such
as `REDIS_ADDR`, region/source configuration, and observability endpoints in an
existing ConfigMap or under `common.env`.

Example production values:

```yaml
image:
  repository: registry.example.com/sekai-master-api
  tag: "1.0.0"

common:
  env:
    APP_ENV: production
    DATABASE_DRIVER: pgx
    REDIS_ADDR: redis.example.internal:6379
  envFrom:
    configMaps: [sekai-master-api-config]
    secrets: [sekai-master-api-runtime]
control:
  envFrom:
    secrets: [sekai-master-api-control]

ingress:
  className: nginx
  public:
    host: master-api.example.com
    tls:
      enabled: true
      secretName: master-api-tls
  control:
    host: master-api-admin.example.com
    tls:
      enabled: true
      secretName: master-api-admin-tls
```

Install or inspect:

```sh
helm upgrade --install sekai-master-api deploy/helm/sekai-master-api \
  --namespace sekai --create-namespace -f production-values.yaml
helm template sekai-master-api deploy/helm/sekai-master-api -f production-values.yaml
```

## Routing and F5 NGINX

The chart uses separate hosts by default:

- Public host: `/api/v1` to `serve`.
- Control host: `/admin`, `/api/v1/admin`, `/api/v1/health`, the exact GitHub
  master-data webhook, and optionally `/docs` to `control`.

The hosts must remain distinct. Otherwise the public `/api/v1` prefix could
overlap control paths. Template rendering fails when both Ingresses are enabled
with the same host. Protect the control host using the network and access policies
appropriate for the cluster. Restrict the webhook and configure its application
secret.

The control Ingress uses F5 annotations, not community ingress-nginx annotations:

```yaml
nginx.org/proxy-buffering: "False"
nginx.org/proxy-read-timeout: "3600s"
nginx.org/proxy-send-timeout: "3600s"
```

These settings support the admin SSE stream. F5 validates annotation values;
timeouts require units. `/docs` is disabled on the control Ingress by default
and the application only serves Swagger in development/test environments.

See the official F5 references for
[Ingress annotations](https://docs.nginx.com/nginx-ingress-controller/configuration/ingress-resources/advanced-configuration-with-annotations/)
and [basic Ingress configuration](https://docs.nginx.com/nginx-ingress-controller/configuration/ingress-resources/basic-configuration/).

## Data and rollout behavior

Redis is the shared data plane. Use persistent or managed Redis with backups.
`serve` cannot repair an empty Redis; after Redis loss, trigger force sync on
`control` to rebuild records and persisted search indexes.

### Redis durability and recovery

Production Redis must be managed/highly available or use durable storage with
both RDB snapshots and AOF persistence. For AOF, use `appendfsync everysec` or a
provider-equivalent policy; this deployment's recovery objective is **RPO ≤ 60
seconds** and **RTO ≤ 30 minutes** for the Redis master-data data plane. Keep
encrypted backups outside the Redis node/PVC, retain daily backups for at least
30 days, and test a restore at least quarterly.

Before re-enabling public traffic after a Redis replacement or restore:

1. Verify PostgreSQL and Redis connectivity, capacity/saturation, and error-rate
   alerts are healthy in the managed-dependency dashboards.
2. Verify the restored Redis keyspace/version state against the expected release
   and configured `MASTER_DATA_REDIS_KEY_PREFIX`.
3. Run force sync through `control` when the cache is absent, stale, or cannot
   be verified; wait for `serve /readyz` and a representative public read to
   return `200`.
4. Record the recovery duration and data/version outcome in the drill record.

Alert on Redis/PostgreSQL connectivity failures, connection saturation, memory
or storage saturation, dependency error rates, and `serve` readiness failures.
The application exposes Redis usage metrics; managed-service metrics should be
scraped from the selected provider or exporter. This chart deliberately does not
embed provider-specific credentials or monitoring resources.

### Schema migration hook

The chart runs a `batch/v1` migration Job before each install and upgrade by
default. The Job runs `sekai-master-api migrate`, waits for the embedded Goose
migrations to finish, and blocks the Helm operation if it fails. Inspect a
failed hook with the release's migration Job and Pod logs before retrying the
release:

```sh
kubectl get jobs -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=migration
kubectl logs job/<release>-sekai-master-api-migrate
```

Configure bounded hook retries and execution time through `migration`:

```yaml
migration:
  enabled: true
  backoffLimit: 1
  activeDeadlineSeconds: 300
  ttlSecondsAfterFinished: 300
  envFrom:
    secrets: [master-api-database]
```

The hook does not use the chart-created ServiceAccount because pre-install hooks
run before ordinary release resources exist. It disables token automounting by
default; set `migration.serviceAccountName` only to an already-existing account.
`migration.envFrom` is intentionally separate from `control.envFrom`, so the
Job receives database configuration without unrelated OIDC, GitHub, or webhook
credentials.

Use expand/contract migrations: release additive, backward-compatible schema
changes before application code that requires them; defer destructive cleanup to
a later release after all old application versions are gone. Helm rollback does
not run Goose down migrations, because an already-applied schema change may be
shared by the restored application version.

### Optional disruption and network controls

Enable PodDisruptionBudgets after selecting values appropriate for the cluster's
capacity. The chart keeps them disabled by default so an existing one-replica
control workload is not made unevictable unexpectedly:

```yaml
podDisruptionBudget:
  serve: { enabled: true, minAvailable: 1 }
  control: { enabled: true, maxUnavailable: 0 }
```

`networkPolicy` is also opt-in because each cluster has different ingress,
PostgreSQL, Redis, OIDC, GitHub, DNS, telemetry, and backup destinations. When
enabled, it selects both application roles and applies the explicitly supplied
native Kubernetes ingress/egress rules. Verify all required dependency traffic
before enabling it in production.

`control.replicaCount` is schema-constrained to `1` and its Deployment uses
`Recreate`, because active-sync locking and state are process-local. Do not make
it horizontally scalable until distributed locking and fencing are implemented.
Deploy `control`, populate Redis, and only then route traffic to `serve`.

### Serve autoscaling

Horizontal Pod Autoscaling is optional and applies only to `serve`. It requires
Kubernetes Metrics Server for CPU/memory resource metrics, or a metrics adapter
for custom/external metrics. CPU autoscaling is enabled in the HPA defaults once
the feature is turned on:

```yaml
serve:
  autoscaling:
    enabled: true
    minReplicas: 2
    maxReplicas: 10
    targetCPUUtilizationPercentage: 70
    targetMemoryUtilizationPercentage: null
```

When autoscaling is enabled, the chart omits `spec.replicas` from the `serve`
Deployment so Helm upgrades do not reset the HPA-managed replica count. Resource
requests must remain configured because utilization targets are calculated from
them. `additionalMetrics` accepts complete `autoscaling/v2` metric entries and
`behavior` accepts the native HPA scaling behavior object. `control` remains
fixed at one replica and never receives an HPA.

The control process writes local payload snapshots. The chart mounts an
ephemeral writable directory at `/app/tmp/master-data-backup` by default so the
read-only root filesystem remains usable. Set
`control.backupVolume.persistentVolumeClaim` to an existing PVC if those
snapshots must survive a control pod replacement; the default `emptyDir` is not
a disaster-recovery backup.

The image entrypoint used for local Compose writes development overrides and
does not forward arguments. Kubernetes therefore invokes the binary directly
with `command: /usr/local/bin/sekai-master-api` and role arguments; this is
intentional and avoids local-development configuration in production.

## Probes

The chart uses root-level operational probe endpoints: `/livez` is process-only,
`/startupz` waits for the role's startup lifecycle, and `/readyz` checks
PostgreSQL. For `serve`, readiness additionally requires all configured regions
to have persisted cards records; a failed or in-progress control sync no longer
blocks readiness once records exist (`regions_pending_sync` in the response is
diagnostic only). Readiness probes are enabled by default.

The `serve` `/readyz` is a bounded, read-only check: it verifies Redis
connectivity once, then requires every configured region to have persisted card
records AND version metadata before reporting the pod ready. If Redis is
unreachable the response reason is `redis` and every configured region is listed
as affected (`unready_regions`); if a region lacks data or version metadata the
reason is `master_data` and only the affected regions are listed. The response
never includes secrets such as database URLs, Redis credentials, or source
repository references.

Startup probes only gate process boot and database migrations via `/startupz`;
they do not wait for master-data sync. The `control` role marks startup complete
after migrations and runs sync in the background, so a long cold-start sync
never causes container restarts. During that window `serve` pods report
degraded through `/readyz` (readiness failures mark the pod NotReady but never
restart it).

## Security and scheduling

The defaults run without root, disable privilege escalation, drop Linux
capabilities, use a read-only root filesystem, and do not mount a service account
token. Resource settings, image pull secrets, pod metadata, affinity,
tolerations, node selectors, and topology spread constraints are configurable.
Use role-specific `envFrom` for control-only credentials; do not expose GitHub
or webhook credentials to public `serve` pods. Changes to externally managed
Secrets and ConfigMaps require a pod rollout because the chart cannot checksum
resources it does not own.

## Extra volumes and volume mounts

The chart supports arbitrary volume and volume-mount overrides via
`common.extraVolumes` / `common.extraVolumeMounts` (shared by all roles) and
per-role equivalents (`serve.extraVolumes`, `control.extraVolumes`, etc.).
Role-level entries are appended after common-level entries using the same
`mergeOverwrite`-style append as `podAnnotations`.

The `control` role mounts two built-in writable volumes by default so the
read-only root filesystem remains usable:

- `/app/tmp/master-data-backup` — local payload snapshots. Override via
  `control.backupVolume.persistentVolumeClaim`.
- `/app/tmp/master-data-sync-resume` — master-data sync resume state. Disable
  with `control.resumeVolume.enabled: false` or switch to a PVC via
  `control.resumeVolume.persistentVolumeClaim`.

## GOMEMLIMIT

The chart automatically sets the `GOMEMLIMIT` environment variable at ~90% of
the container memory limit to cap Go GC heap usage. For example, with the
default 1Gi limit, `GOMEMLIMIT` is set to `966367641` (≈922 MiB).

Supported memory-limit suffixes: plain integer bytes, `Ki`, `Mi`, `Gi`, `Ti`
(binary, ×1024 chain) and decimal `K`, `M`, `G`, `T`. Quantities with a
fractional numeric part (e.g. `1.5Gi`) or unknown suffixes are silently
ignored — no `GOMEMLIMIT` is injected for invalid memory-limit values. Use
whole-number quantities like `1536Mi` instead.

To override, set `GOMEMLIMIT` explicitly in `common.env`, the role's `env`, or
any `extraEnv` list. The auto-derived value is skipped whenever `GOMEMLIMIT` is
already present in any of those sources.

## Graceful shutdown

On `SIGTERM`/`SIGINT` the process:

1. Stops accepting new connections and drains in-flight HTTP requests (the
   Gin `*http.Server` is shut down with a bounded timeout).
2. Cancels the application lifecycle context, which interrupts background sync
   workers (`StartSync`, webhook `SyncRegion`, startup auto-sync/recovery) so
   they stop instead of being orphaned.
3. Waits for the lifecycle goroutines and any in-flight sync to finish, bounded
   by the shutdown timeout.
4. Closes dependencies in order — first the OTel periodic callbacks/flush, then
   Redis cache, then the database — and finally flushes the log buffers.

The bounded shutdown window is controlled by `SHUTDOWN_TIMEOUT_SECONDS` (sourced
from `common.shutdownTimeoutSeconds`, default `25`). It must stay **smaller** than
the pod's `common.terminationGracePeriodSeconds` (default `30`) so the process
finishes and exits cleanly before Kubernetes sends `SIGKILL`. The deadline timer
starts only when the shutdown signal is received, not at process start, so the
grace period is not consumed during normal runtime.

### Configuring the shutdown timeout

`SHUTDOWN_TIMEOUT_SECONDS` is injected from `common.shutdownTimeoutSeconds`
(default `25`) as an explicit `env` entry on every pod (`serve`, `control`, and the
migration Job). This field is the single source for that variable — do not set
`SHUTDOWN_TIMEOUT_SECONDS` in `common.env`, a role's `env`, or any `extraEnv` list;
the chart fails at template time if you do.

The chart enforces the relationship that JSON Schema cannot express: at template
time it fails if `common.shutdownTimeoutSeconds` is greater than or equal to
`common.terminationGracePeriodSeconds` (default `30`). `values.schema.json` still
constrains the type and a soft upper bound (`maximum: 30`), but that upper bound
does not, on its own, enforce the strict inequality against the grace period. Set
`common.shutdownTimeoutSeconds` lower than the grace period to avoid a mid-drain
`SIGKILL`.

> **External override caveat (accurate):** Kubernetes applies explicit `env`
> entries after `envFrom`, and for a given variable the explicit `env` value wins
> over any `envFrom` source. Because the chart renders `SHUTDOWN_TIMEOUT_SECONDS`
> as an explicit `env` entry, an external ConfigMap or Secret referenced through
> `common.envFrom` / `role.envFrom` that also sets `SHUTDOWN_TIMEOUT_SECONDS` is
> shadowed at runtime and has no effect. To change the shutdown timeout, set
> `common.shutdownTimeoutSeconds` in values; the schema validation only governs
> the in-chart value.

In-flight syncs interrupted by shutdown are left in a recoverable (`running`/`pending`)
status rather than marked failed, so interrupted-sync recovery can resume them on
the next start. A second `SIGTERM`/`SIGINT` during shutdown force-closes remaining
HTTP connections immediately instead of being silently ignored.
