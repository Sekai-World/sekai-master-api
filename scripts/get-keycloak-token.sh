#!/usr/bin/env sh

set -eu

KEYCLOAK_PORT="${KEYCLOAK_PORT:-18081}"
KEYCLOAK_BASE_URL="${KEYCLOAK_BASE_URL:-http://localhost:${KEYCLOAK_PORT}}"
KEYCLOAK_REALM="${KEYCLOAK_REALM:-sekai}"
KEYCLOAK_CLIENT_ID="${KEYCLOAK_CLIENT_ID:-sekai-api}"
KEYCLOAK_USERNAME="${KEYCLOAK_USERNAME:-alice}"
KEYCLOAK_PASSWORD="${KEYCLOAK_PASSWORD:-alice123!}"

curl -sS -X POST "${KEYCLOAK_BASE_URL}/realms/${KEYCLOAK_REALM}/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=password" \
  --data-urlencode "client_id=${KEYCLOAK_CLIENT_ID}" \
  --data-urlencode "username=${KEYCLOAK_USERNAME}" \
  --data-urlencode "password=${KEYCLOAK_PASSWORD}"
