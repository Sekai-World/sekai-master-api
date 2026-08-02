#!/usr/bin/env sh

set -eu

if [ "${REDIS_RECOVERY_DRILL_CONFIRM:-}" != "DELETE_PREFIXED_REDIS_DATA" ]; then
  echo "[redis-recovery-drill] set REDIS_RECOVERY_DRILL_CONFIRM=DELETE_PREFIXED_REDIS_DATA to continue"
  exit 2
fi

: "${REDIS_ADDR:?REDIS_ADDR is required}"
: "${MASTER_DATA_REDIS_KEY_PREFIX:?MASTER_DATA_REDIS_KEY_PREFIX is required}"
: "${REDIS_RECOVERY_REGION:?REDIS_RECOVERY_REGION is required}"
: "${REDIS_RECOVERY_PUBLIC_URL:?REDIS_RECOVERY_PUBLIC_URL is required}"
: "${REDIS_RECOVERY_SERVE_URL:?REDIS_RECOVERY_SERVE_URL is required}"
: "${REDIS_RECOVERY_CONTROL_URL:?REDIS_RECOVERY_CONTROL_URL is required}"
: "${ADMIN_BEARER_TOKEN:?ADMIN_BEARER_TOKEN is required}"

REDIS_CLI="${REDIS_CLI:-redis-cli}"
export REDISCLI_AUTH="${REDIS_PASSWORD:-}"
CURL_CONNECT_TIMEOUT_SECONDS="${CURL_CONNECT_TIMEOUT_SECONDS:-5}"
CURL_MAX_TIME_SECONDS="${CURL_MAX_TIME_SECONDS:-15}"
CURL_STATUS_FORMAT='%{http_code}'

validate_curl_timeout() {
  timeout_name="$1"
  timeout_value="$2"

  case "${timeout_value}" in
    ''|*[!0-9]*)
      echo "[redis-recovery-drill] ${timeout_name} must be a positive whole-second value from 1 through 60"
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
      echo "[redis-recovery-drill] ${timeout_name} must be a positive whole-second value from 1 through 60"
      exit 2
      ;;
  esac
}

validate_curl_timeout CURL_CONNECT_TIMEOUT_SECONDS "${CURL_CONNECT_TIMEOUT_SECONDS}"
validate_curl_timeout CURL_MAX_TIME_SECONDS "${CURL_MAX_TIME_SECONDS}"
scan_file="$(mktemp)"

cleanup() {
  rm -f "${scan_file}"
}

trap cleanup EXIT HUP INT TERM

redis() {
  "${REDIS_CLI}" -u "redis://${REDIS_ADDR}" "$@"
}

status() {
  endpoint="$1"
  curl -sS --connect-timeout "${CURL_CONNECT_TIMEOUT_SECONDS}" --max-time "${CURL_MAX_TIME_SECONDS}" \
    -o /dev/null -w "${CURL_STATUS_FORMAT}" "${endpoint}" || true
}

wait_for_status() {
  expected="$1"
  endpoint="$2"
  attempt=0

  while :; do
    [ "$(status "${endpoint}")" = "${expected}" ] && return 0
    attempt=$((attempt + 1))
    if [ "${attempt}" -ge 60 ]; then
      echo "[redis-recovery-drill] expected HTTP ${expected} from ${endpoint}"
      return 1
    fi
    sleep 1
  done
}

if [ "$(status "${REDIS_RECOVERY_PUBLIC_URL}")" != "200" ]; then
  echo "[redis-recovery-drill] representative public read must return HTTP 200 before deletion"
  exit 1
fi

if ! redis --scan >"${scan_file}"; then
  echo "[redis-recovery-drill] failed to scan Redis keys"
  exit 1
fi

deleted=0
prefix_length=${#MASTER_DATA_REDIS_KEY_PREFIX}
while IFS= read -r key || [ -n "${key}" ]; do
  [ -n "${key}" ] || continue
  key_prefix="$(printf '%s' "${key}" | cut -c "1-${prefix_length}")"
  [ "${key_prefix}" = "${MASTER_DATA_REDIS_KEY_PREFIX}" ] || continue
  redis UNLINK "${key}" >/dev/null
  deleted=$((deleted + 1))
done <"${scan_file}"

echo "[redis-recovery-drill] removed ${deleted} keys with configured prefix"
wait_for_status 503 "${REDIS_RECOVERY_SERVE_URL}/readyz"

curl -fsS --connect-timeout "${CURL_CONNECT_TIMEOUT_SECONDS}" --max-time "${CURL_MAX_TIME_SECONDS}" -X POST \
  -H "Authorization: Bearer ${ADMIN_BEARER_TOKEN}" \
  "${REDIS_RECOVERY_CONTROL_URL}/api/v1/admin/master-data/sync/force" >/dev/null

wait_for_status 200 "${REDIS_RECOVERY_SERVE_URL}/readyz"
wait_for_status 200 "${REDIS_RECOVERY_PUBLIC_URL}"
echo "[redis-recovery-drill] recovery completed for ${REDIS_RECOVERY_REGION}"
