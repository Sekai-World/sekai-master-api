#!/usr/bin/env sh

set -eu

APP_PORT="${APP_PORT:-18080}"
SMOKE_BASE_URL="${SMOKE_BASE_URL:-http://localhost:${APP_PORT}}"
SMOKE_SERVE_BASE_URL="${SMOKE_SERVE_BASE_URL:-${SMOKE_BASE_URL}}"
SMOKE_CONTROL_BASE_URL="${SMOKE_CONTROL_BASE_URL:-}"
SMOKE_PUBLIC_PATH="${SMOKE_PUBLIC_PATH:-}"
ADMIN_BEARER_TOKEN="${ADMIN_BEARER_TOKEN:-}"
SMOKE_CHECK_PROTECTED="${SMOKE_CHECK_PROTECTED:-false}"

wait_for_status() {
  expected_status="$1"
  endpoint="$2"
  attempt=0

  while :; do
    status="$(curl -sS -o /dev/null -w '%{http_code}' "${endpoint}" || true)"
    [ "${status}" = "${expected_status}" ] && return 0

    attempt=$((attempt + 1))
    if [ "${attempt}" -ge 60 ]; then
      echo "[smoke] expected HTTP ${expected_status} from ${endpoint}, got ${status}"
      return 1
    fi
    sleep 1
  done
}

require_status() {
  expected_status="$1"
  endpoint="$2"
  status="$(curl -sS -o /dev/null -w '%{http_code}' "${endpoint}" || true)"
  if [ "${status}" != "${expected_status}" ]; then
    echo "[smoke] expected HTTP ${expected_status} from ${endpoint}, got ${status}"
    exit 1
  fi
}

require_rejected() {
  method="$1"
  endpoint="$2"
  status="$(curl -sS -o /dev/null -w '%{http_code}' -X "${method}" "${endpoint}" || true)"
  case "${status}" in
    2??)
      echo "[smoke] expected ${endpoint} to reject an unauthenticated request, got HTTP ${status}"
      exit 1
      ;;
  esac
}

echo "[smoke] waiting for startup and readiness"
wait_for_status 200 "${SMOKE_SERVE_BASE_URL}/startupz"
wait_for_status 200 "${SMOKE_SERVE_BASE_URL}/readyz"

if [ -z "${SMOKE_PUBLIC_PATH}" ]; then
  echo "[smoke] SMOKE_PUBLIC_PATH is required, for example /api/v1/versions/jp"
  exit 2
fi

require_status 200 "${SMOKE_SERVE_BASE_URL}${SMOKE_PUBLIC_PATH}"

if [ "${SMOKE_CHECK_PROTECTED}" = "true" ]; then
  if [ -z "${ADMIN_BEARER_TOKEN}" ] || [ -z "${SMOKE_CONTROL_BASE_URL}" ]; then
    echo "[smoke] ADMIN_BEARER_TOKEN and SMOKE_CONTROL_BASE_URL are required when SMOKE_CHECK_PROTECTED=true"
    exit 2
  fi

  require_status 404 "${SMOKE_CONTROL_BASE_URL}${SMOKE_PUBLIC_PATH}"
  require_status 401 "${SMOKE_CONTROL_BASE_URL}/api/v1/admin/master-data/events"
  require_rejected POST "${SMOKE_CONTROL_BASE_URL}/api/v1/internal/github/webhooks/master-data"
  protected_status="$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer ${ADMIN_BEARER_TOKEN}" "${SMOKE_CONTROL_BASE_URL}/api/v1/admin/profile" || true)"
  if [ "${protected_status}" != "200" ]; then
    echo "[smoke] expected authenticated admin profile to return HTTP 200, got ${protected_status}"
    exit 1
  fi
fi

echo "[smoke] success"
