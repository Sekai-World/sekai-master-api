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
- Control host: `/admin`, `/api/v1/admin`, the exact GitHub master-data webhook,
  and optionally `/docs` to `control`.

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
PostgreSQL. For `serve`, readiness additionally requires
all configured regions to have successful persisted sync state and Redis-backed
data readiness. Readiness probes are enabled by default.

## Security and scheduling

The defaults run without root, disable privilege escalation, drop Linux
capabilities, use a read-only root filesystem, and do not mount a service account
token. Resource settings, image pull secrets, pod metadata, affinity,
tolerations, node selectors, and topology spread constraints are configurable.
Use role-specific `envFrom` for control-only credentials; do not expose GitHub
or webhook credentials to public `serve` pods. Changes to externally managed
Secrets and ConfigMaps require a pod rollout because the chart cannot checksum
resources it does not own.
