# Production Observability on k3s

The compose observability stack is for local development. In k3s, the deployed Grafana Alloy instance can satisfy the collector role if it receives app OTLP traffic and forwards every required signal to the right backend.

## Signal Flow

```text
App -> Alloy OTLP receiver -> Tempo
App -> Alloy OTLP receiver -> Prometheus OTLP endpoint
Pod stdout/stderr -> Alloy Kubernetes log source -> Loki
Kubernetes events -> Alloy Kubernetes event source -> Loki
Grafana -> Tempo / Loki / Prometheus
```

## Alloy Requirements

Alloy must provide:

- OTLP gRPC on `4317` and OTLP HTTP on `4318`.
- Metrics forwarding to Prometheus through `/api/v1/otlp`.
- Trace forwarding to Tempo.
- Pod log forwarding to Loki.
- Kubernetes event forwarding to Loki.
- Cluster-private ingestion ports.
- Resource requests/limits and monitoring for dropped data, refused data, queue pressure, memory usage, and exporter failures.

The current deployed Alloy config already covers pod logs, Kubernetes events, OTLP ingest, and metric export. It still needs Tempo export if traces currently point only to `otelcol.exporter.debug`.

## Trace Export

Do not leave production traces on the debug exporter only:

```hcl
output {
  metrics = [otelcol.exporter.otlphttp.prometheus.input]
  traces  = [otelcol.exporter.debug.default.input]
}
```

Add a Tempo exporter and route traces to it:

```hcl
otelcol.exporter.otlp "tempo" {
  client {
    endpoint = "tempo.monitoring.svc.cluster.local:4317"

    tls {
      insecure = true
    }
  }
}

otelcol.receiver.otlp "default" {
  grpc {
    endpoint = "0.0.0.0:4317"
  }

  http {
    endpoint = "0.0.0.0:4318"
  }

  output {
    metrics = [otelcol.exporter.otlphttp.prometheus.input]
    traces  = [otelcol.exporter.otlp.tempo.input]
  }
}
```

Keep `otelcol.exporter.debug` only for temporary troubleshooting.

## Metrics Export

The deployed metrics exporter is acceptable if Prometheus has OTLP receive enabled:

```hcl
otelcol.exporter.otlphttp "prometheus" {
  client {
    endpoint = "http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090/api/v1/otlp"

    tls {
      insecure = true
    }
  }
}
```

Confirm Prometheus is configured to accept OTLP writes. Otherwise, app metrics will be accepted by Alloy but rejected by Prometheus.

## Logs

If Alloy collects pod stdout/stderr and forwards to Loki, keep the app's direct Loki push disabled to avoid duplicate logs:

```env
LOKI_PUSH_URL=
```

Keep Loki labels low-cardinality. Labels such as `cluster`, `namespace`, `pod`, `container`, `node`, `app`, and `job` are acceptable for Kubernetes logs. Do not promote `trace_id`, `span_id`, request IDs, user IDs, paths, entity IDs, or raw errors into labels.

The app writes structured logs with `trace_id` and `span_id` in the log body, which supports Grafana derived fields without making them labels.

## App Configuration

Set these from k3s ConfigMaps / Secrets / manifests:

```env
APP_ENV=production
LOG_LEVEL=info
OTEL_ENABLED=true
OTEL_SERVICE_NAME=sekai-master-api
OTEL_SERVICE_VERSION=<image-or-git-version>
OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.monitoring.svc.cluster.local:4318
OTEL_EXPORTER_OTLP_INSECURE=true
LOKI_PUSH_URL=
```

If TLS is terminated at Alloy or an OTLP gateway:

```env
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.internal
OTEL_EXPORTER_OTLP_INSECURE=false
```

## Tempo

- Use durable object storage for traces.
- Configure explicit retention.
- Keep Tempo ingestion/query ports private unless protected by internal networking and auth.
- Expose traces through Grafana, not directly to the public internet.

## Grafana

- Configure Prometheus, Loki, and Tempo datasources.
- Configure the Loki datasource with a derived field that extracts `trace_id` and links to Tempo.
- Expose Grafana through authenticated ingress only.
- Provision datasources and dashboards through infra manifests.
