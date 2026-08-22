# Development Observability Images

Major updates to the development observability images need runtime
configuration checks in addition to the repository CI suite.

## Confirmed compatibility checks

- `prom/prometheus:v3.13.1` accepts `deploy/compose/prometheus/prometheus.yml`;
  verify with the image's `/bin/promtool check config` command.
- `grafana/grafana-oss:13.0.2` starts successfully with the existing
  provisioning and dashboard directories. The health endpoint responds, the
  Loki/Prometheus/Tempo data sources are provisioned, and dashboard provisioning
  completes.
- `grafana/tempo:3.0.2` does **not** accept the existing
  `deploy/compose/tempo/tempo.yaml`. Its config verification reports
  `field compactor not found in type app.Config` for the top-level `compactor`
  block. Migrate and re-verify the Tempo configuration before adopting Tempo 3.

Source: runtime Docker smoke tests performed while reviewing Renovate PRs #33,
#31, and #32 on 2026-07-17.
