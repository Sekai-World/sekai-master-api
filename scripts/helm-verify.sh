#!/usr/bin/env sh

set -eu

# Verifies the chart lints and renders correctly for the three tracked value
# sets: chart defaults, the production-safety profile, and the development
# profile. Also proves that values.schema.json rejects the unsafe
# configurations the profiles are meant to prevent.
#
# Requires helm on PATH. Run locally with `mise run helm-verify`; CI runs the
# same script in the Helm Verify job.

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)/deploy/helm/sekai-master-api"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

RELEASE_NAME="helm-verify"
RELEASE_NAMESPACE="helm-verify"
RENDER=""

fail() {
  message="$1"
  echo "[helm-verify] FAIL: $message" >&2
  exit 1
}

ok() {
  message="$1"
  echo "[helm-verify] ok: $message"
}

expect_contains() {
  file="$1"
  pattern="$2"
  description="$3"
  grep -qF -- "$pattern" "$file" || fail "rendered output missing $description (pattern: $pattern)"
  ok "$description"
}

expect_not_contains() {
  file="$1"
  pattern="$2"
  description="$3"
  grep -qF -- "$pattern" "$file" && fail "rendered output unexpectedly contains $description (pattern: $pattern)"
  ok "$description"
}

expect_count() {
  file="$1"
  pattern="$2"
  want="$3"
  description="$4"
  got="$(grep -cF -- "$pattern" "$file" || true)"
  [ "$got" = "$want" ] || fail "expected $want occurrence(s) of $description, got $got (pattern: $pattern)"
  ok "$want x $description"
}

lint() {
  profile="$1"
  label="${profile:-chart defaults}"
  if [ -n "$profile" ]; then
    helm lint "$CHART_DIR" -f "$CHART_DIR/$profile" >/dev/null 2>&1 || fail "helm lint failed for $label"
  else
    helm lint "$CHART_DIR" >/dev/null 2>&1 || fail "helm lint failed for $label"
  fi
  ok "helm lint $label"
}

render() {
  profile="$1"
  name="$2"
  label="${profile:-chart defaults}"
  RENDER="$WORK_DIR/render-$name.yaml"
  if [ -n "$profile" ]; then
    helm template "$RELEASE_NAME" "$CHART_DIR" --namespace "$RELEASE_NAMESPACE" -f "$CHART_DIR/$profile" >"$RENDER" || fail "helm template failed for $label"
  else
    helm template "$RELEASE_NAME" "$CHART_DIR" --namespace "$RELEASE_NAMESPACE" >"$RENDER" || fail "helm template failed for $label"
  fi
}

expect_schema_reject() {
  overlay="$1"
  description="$2"
  overlay_file="$WORK_DIR/schema-negative.yaml"
  printf '%s\n' "$overlay" >"$overlay_file"
  if helm template "$RELEASE_NAME" "$CHART_DIR" --namespace "$RELEASE_NAMESPACE" -f "$overlay_file" >/dev/null 2>&1; then
    fail "schema accepted $description"
  fi
  ok "schema rejects $description"
}

# --- Chart defaults (safe baseline: policies off, no TLS) -------------------

lint ""
render "" defaults

expect_count "$RENDER" "kind: PodDisruptionBudget" 0 "PodDisruptionBudget"
expect_count "$RENDER" "kind: NetworkPolicy" 0 "NetworkPolicy"
expect_not_contains "$RENDER" "topologySpreadConstraints" "topology constraints in default render"
expect_count "$RENDER" "kind: Ingress" 2 "Ingress resources"
expect_not_contains "$RENDER" "secretName" "TLS in default render"
expect_count "$RENDER" "  replicas: 2" 1 "serve replicas=2"
expect_count "$RENDER" "  replicas: 1" 1 "control replicas=1"
expect_contains "$RENDER" "type: Recreate" "control Recreate upgrade strategy"

# --- Production-safety profile ----------------------------------------------

lint "values-production.yaml"
render "values-production.yaml" production

expect_count "$RENDER" "kind: PodDisruptionBudget" 2 "PodDisruptionBudgets (serve + control)"
expect_contains "$RENDER" "  minAvailable: 1" "serve PDB keeping one replica available"
expect_contains "$RENDER" "  maxUnavailable: 0" "control PDB blocking voluntary disruption"
expect_count "$RENDER" "kind: NetworkPolicy" 1 "NetworkPolicy"
expect_contains "$RENDER" "port: 8080" "NetworkPolicy ingress to the application port"
for port in 53 5432 6379 443 4317; do
  expect_contains "$RENDER" "port: $port" "NetworkPolicy egress to port $port"
done
expect_contains "$RENDER" "topologyKey: kubernetes.io/hostname" "hostname topology spread"
expect_contains "$RENDER" "topologyKey: topology.kubernetes.io/zone" "zone topology spread"
expect_count "$RENDER" "whenUnsatisfiable: ScheduleAnyway" 6 "soft topology constraints (deployments + migration Job)"
expect_contains "$RENDER" "secretName: \"master-api-tls\"" "TLS on the public ingress"
expect_contains "$RENDER" "secretName: \"master-api-admin-tls\"" "TLS on the control ingress"
expect_contains "$RENDER" "name: \"master-api-database\"" "credentials injected through envFrom secret refs"
expect_not_contains "$RENDER" "DATABASE_PASSWORD" "plaintext database credentials"
expect_count "$RENDER" "  replicas: 1" 1 "control replicas=1 (single-writer preserved)"

# --- Development profile ------------------------------------------------------

lint "values-development.yaml"
render "values-development.yaml" development

expect_count "$RENDER" "kind: Ingress" 0 "Ingress resources"
expect_count "$RENDER" "kind: Job" 0 "migration Job"
expect_contains "$RENDER" "value: \"development\"" "APP_ENV=development"
expect_contains "$RENDER" "value: \"sqlite\"" "SQLite storage driver"
expect_contains "$RENDER" "type: RollingUpdate" "control RollingUpdate under coordination (development profile)"
expect_not_contains "$RENDER" "type: Recreate" "no Recreate strategy under coordination (development profile)"

# --- Schema enforcement (unsafe values must fail) ----------------------------

expect_schema_reject "podDisruptionBudget:
  control:
    enabled: true
    maxUnavailable: 1" "control PDB enabled with maxUnavailable 1"

expect_schema_reject "networkPolicy:
  enabled: true" "NetworkPolicy enabled without ingress/egress rules"

expect_schema_reject "ingress:
  public:
    tls:
      enabled: true" "TLS enabled without a secretName"

expect_schema_reject "control:
  replicaCount: 2" "control scaled beyond one replica without coordination"

# --- Coordinated control (test-environment capability) ----------------------

# Multi-replica control is only legal with coordination enabled; the render
# must then use RollingUpdate and honor the raised replica count.
render_coordinated="$WORK_DIR/render-coordinated.yaml"
helm template "$RELEASE_NAME" "$CHART_DIR" --namespace "$RELEASE_NAMESPACE" \
  --set control.coordination.enabled=true \
  --set control.replicaCount=2 >"$render_coordinated" 2>/dev/null \
  || fail "coordinated control render failed"
RENDER="$render_coordinated"
expect_count "$RENDER" "  replicas: 2" 2 "coordinated control renders two replicas (serve + control)"
expect_not_contains "$RENDER" "type: Recreate" "no Recreate strategy under coordination"

echo "[helm-verify] all checks passed"
