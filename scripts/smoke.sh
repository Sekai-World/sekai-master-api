#!/usr/bin/env sh

set -eu

APP_PORT="${APP_PORT:-8080}"
APP_ENV="${APP_ENV:-test}"
DATABASE_URL="${DATABASE_URL:-postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable}"
ZITADEL_ISSUER_URL="${ZITADEL_ISSUER_URL:-https://zitadel.example.com}"
ZITADEL_AUDIENCE="${ZITADEL_AUDIENCE:-https://api.example.com}"
ADMIN_BEARER_TOKEN="${ADMIN_BEARER_TOKEN:-}"

API_PID=""

cleanup() {
  if [ -n "${API_PID}" ]; then
    kill "${API_PID}" >/dev/null 2>&1 || true
    wait "${API_PID}" 2>/dev/null || true
  fi
}

trap cleanup EXIT INT TERM

echo "[smoke] starting api"
if curl -fsS "http://localhost:${APP_PORT}/api/v1/health" >/dev/null 2>&1; then
  echo "[smoke] api port ${APP_PORT} is already in use, stop existing process or set APP_PORT"
  exit 1
fi

APP_ENV="${APP_ENV}" \
DATABASE_URL="${DATABASE_URL}" \
ZITADEL_ISSUER_URL="${ZITADEL_ISSUER_URL}" \
ZITADEL_AUDIENCE="${ZITADEL_AUDIENCE}" \
APP_PORT="${APP_PORT}" \
go run ./cmd/api >/tmp/sekai-smoke-api.log 2>&1 &
API_PID=$!

echo "[smoke] waiting api ready"
attempt=0
until curl -fsS "http://localhost:${APP_PORT}/api/v1/health" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 60 ]; then
    echo "[smoke] api not ready in time"
    cat /tmp/sekai-smoke-api.log || true
    exit 1
  fi
  sleep 1
done

if [ -z "${ADMIN_BEARER_TOKEN}" ]; then
  echo "[smoke] ADMIN_BEARER_TOKEN is required for protected endpoint verification"
  exit 1
fi

echo "[smoke] calling protected endpoint"
profile_response="$(curl -fsS -H "Authorization: Bearer ${ADMIN_BEARER_TOKEN}" "http://localhost:${APP_PORT}/api/v1/admin/profile")"

echo "[smoke] success"
printf '%s\n' "${profile_response}"
