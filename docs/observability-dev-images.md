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
- `grafana/tempo:3.0.3` accepts the migrated
  `deploy/compose/tempo/tempo.yaml`. Tempo 3 monolithic mode uses
  `target: all` and `backend_worker.compaction.block_retention` for the
  24-hour retention setting; verify it with the image's `-config.verify`
  command and a short startup smoke test.

Source: runtime Docker smoke tests performed while reviewing Renovate PRs #33, #31, and #32 on 2026-07-17; the Tempo 3.0.3 migration was re-verified on 2026-09-06.
