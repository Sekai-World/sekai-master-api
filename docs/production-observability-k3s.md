# Production Observability on k3s

The compose observability stack is for local development. In k3s / production, keep the same signal flow but run it as cluster infrastructure.

## Signal Flow

```text
App -> OpenTelemetry Collector -> Tempo
App -> OpenTelemetry Collector -> metrics backend
App -> Loki
Grafana -> Tempo / Loki / metrics backend
```

## OpenTelemetry Collector

- Run an in-cluster Collector service for app OTLP ingest, for example `http://otel-collector:4318`.
- Keep Collector ingestion ports cluster-private.
- Use resource requests/limits and monitor dropped spans, refused spans, queue pressure, memory usage, and exporter failures.
- Add HPA or multiple replicas if production traffic needs it.
- Use tail sampling:
  - keep error traces
  - keep slow traces
  - keep a small baseline sample for normal requests
- Tune sampling thresholds and percentages from real traffic.

## Tempo

- Use durable object storage for traces.
- Configure explicit retention.
- Keep Tempo ingestion/query ports private unless protected by internal networking and auth.
- Expose traces through Grafana, not directly to the public internet.

## Loki

- Keep labels low-cardinality only: `service`, `env`, `level`, `component`.
- Do not use `trace_id`, `span_id`, request IDs, user IDs, paths, entity IDs, or raw errors as labels.
- Keep `trace_id` and `span_id` inside the structured log body.

## Grafana

- Configure the Loki datasource with a derived field that extracts `trace_id` and links to Tempo.
- Expose Grafana through authenticated ingress only.
- Provision Tempo, Loki, and metrics datasources through infra manifests.

## App Configuration

Set these from k3s ConfigMaps / Secrets / manifests:

```env
APP_ENV=production
LOG_LEVEL=info
OTEL_ENABLED=true
OTEL_SERVICE_NAME=sekai-master-api
OTEL_SERVICE_VERSION=<image-or-git-version>
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
OTEL_EXPORTER_OTLP_INSECURE=true
LOKI_PUSH_URL=http://loki:3100/loki/api/v1/push
```

If TLS is terminated at the Collector or gateway:

```env
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.internal
OTEL_EXPORTER_OTLP_INSECURE=false
```

## Optional Node-Level Collection

Use Grafana Alloy, Grafana Agent, or OpenTelemetry Collector as a DaemonSet only if node-level logs, host metrics, or Kubernetes events are needed. The API app itself only needs the Collector service endpoint and Loki push endpoint.
