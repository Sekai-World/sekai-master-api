#!/usr/bin/env sh

set -eu

APP_PORT="${APP_PORT:-18080}"
SMOKE_BASE_URL="${SMOKE_BASE_URL:-http://localhost:${APP_PORT}}"
SMOKE_SERVE_BASE_URL="${SMOKE_SERVE_BASE_URL:-${SMOKE_BASE_URL}}"
SMOKE_CONTROL_BASE_URL="${SMOKE_CONTROL_BASE_URL:-}"
SMOKE_PUBLIC_PATH="${SMOKE_PUBLIC_PATH:-}"
ADMIN_BEARER_TOKEN="${ADMIN_BEARER_TOKEN:-}"
SMOKE_CHECK_PROTECTED="${SMOKE_CHECK_PROTECTED:-false}"
CURL_CONNECT_TIMEOUT_SECONDS="${CURL_CONNECT_TIMEOUT_SECONDS:-5}"
CURL_MAX_TIME_SECONDS="${CURL_MAX_TIME_SECONDS:-15}"
CURL_STATUS_FORMAT='%{http_code}'

validate_curl_timeout() {
  timeout_name="$1"
  timeout_value="$2"

  case "${timeout_value}" in
    ''|*[!0-9]*)
      echo "[smoke] ${timeout_name} must be a positive whole-second value from 1 through 60"
      exit 2
      ;;
  esac

  normalized_timeout="${timeout_value}"
  while [ "${normalized_timeout#0}" != "${normalized_timeout}" ]; do
    normalized_timeout="${normalized_timeout#0}"
  done

  case "${normalized_timeout}" in
    [1-9]|[1-5][0-9]|60) ;;
    *)
      echo "[smoke] ${timeout_name} must be a positive whole-second value from 1 through 60"
      exit 2
      ;;
  esac
}

validate_curl_timeout CURL_CONNECT_TIMEOUT_SECONDS "${CURL_CONNECT_TIMEOUT_SECONDS}"
validate_curl_timeout CURL_MAX_TIME_SECONDS "${CURL_MAX_TIME_SECONDS}"

request_status() {
  method="$1"
  endpoint="$2"
  shift 2
  curl -sS --connect-timeout "${CURL_CONNECT_TIMEOUT_SECONDS}" --max-time "${CURL_MAX_TIME_SECONDS}" \
    -o /dev/null -w "${CURL_STATUS_FORMAT}" -X "${method}" "$@" "${endpoint}" || true
}

wait_for_status() {
  expected_status="$1"
  endpoint="$2"
  attempt=0

  while :; do
    status="$(request_status GET "${endpoint}")"
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
  status="$(request_status GET "${endpoint}")"
  if [ "${status}" != "${expected_status}" ]; then
    echo "[smoke] expected HTTP ${expected_status} from ${endpoint}, got ${status}"
    exit 1
  fi
}

require_rejected() {
  method="$1"
  endpoint="$2"
  status="$(request_status "${method}" "${endpoint}")"
  case "${status}" in
    401|503) ;;
    *)
      echo "[smoke] expected ${endpoint} to reject an unauthenticated request with HTTP 401 or 503, got ${status}"
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
  protected_status="$(printf 'Authorization: Bearer %s\n' "${ADMIN_BEARER_TOKEN}" | request_status GET "${SMOKE_CONTROL_BASE_URL}/api/v1/admin/profile" -H @-)"
  if [ "${protected_status}" != "200" ]; then
    echo "[smoke] expected authenticated admin profile to return HTTP 200, got ${protected_status}"
    exit 1
  fi
fi

echo "[smoke] success"
