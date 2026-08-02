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

redis() {
  "${REDIS_CLI}" -u "redis://${REDIS_ADDR}" "$@"
}

status() {
  curl -sS -o /dev/null -w '%{http_code}' "$1" || true
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

deleted=0
"${REDIS_CLI}" -u "redis://${REDIS_ADDR}" --scan --pattern "${MASTER_DATA_REDIS_KEY_PREFIX}*" |
while IFS= read -r key; do
  [ -n "${key}" ] || continue
  redis UNLINK "${key}" >/dev/null
  deleted=$((deleted + 1))
done

echo "[redis-recovery-drill] removed all keys with configured prefix"
wait_for_status 503 "${REDIS_RECOVERY_SERVE_URL}/readyz"

curl -fsS -X POST \
  -H "Authorization: Bearer ${ADMIN_BEARER_TOKEN}" \
  "${REDIS_RECOVERY_CONTROL_URL}/api/v1/admin/master-data/sync/force" >/dev/null

wait_for_status 200 "${REDIS_RECOVERY_SERVE_URL}/readyz"
wait_for_status 200 "${REDIS_RECOVERY_PUBLIC_URL}"
echo "[redis-recovery-drill] recovery completed for ${REDIS_RECOVERY_REGION}"
