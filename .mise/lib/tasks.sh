#!/usr/bin/env sh

set -eu

repo_root() {
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

load_defaults() {
  APP_NAME="${APP_NAME:-sekai-master-api}"
  COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose/dev-compose.yaml}"
  APP_PORT="${APP_PORT:-8080}"
  KEYCLOAK_PORT="${KEYCLOAK_PORT:-18081}"
  LOKI_PORT="${LOKI_PORT:-3100}"
  COMPOSE_HOST="${COMPOSE_HOST:-host.docker.internal}"
  DEV_APP_IMAGE="${DEV_APP_IMAGE:-sekai/sekai-master-api-dev:local}"
  DEV_APP_CONTAINER="${DEV_APP_CONTAINER:-sekai-master-api-dev}"
  DEV_APP_VOLUME="${DEV_APP_VOLUME:-sekai-master-api-dev-data}"
  DEV_APP_NETWORK="${DEV_APP_NETWORK:-sekai-dev}"
  DEV_APP_INTERNAL_PORT="${DEV_APP_INTERNAL_PORT:-8080}"
  DEV_MASTER_DATA_AUTO_SYNC="${DEV_MASTER_DATA_AUTO_SYNC:-false}"
  DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC="${DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC:-false}"
  DOCKER="${DOCKER:-docker}"
  APP_ENV="${APP_ENV:-development}"
  GO_DOCKER_IMAGE="${GO_DOCKER_IMAGE:-golang:1.26.2-alpine3.23}"
  GO_DOCKER_WORKDIR="${GO_DOCKER_WORKDIR:-/src}"
  GO_DOCKER_MOD_CACHE_VOLUME="${GO_DOCKER_MOD_CACHE_VOLUME:-sekai-master-api-go-mod-cache}"
  GO_DOCKER_BUILD_CACHE_VOLUME="${GO_DOCKER_BUILD_CACHE_VOLUME:-sekai-master-api-go-build-cache}"
  GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME="${GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME:-sekai-master-api-go-toolchain-cache}"
}

load_development_env() {
  set -a
  [ ! -f "./.env" ] || . "./.env"
  [ ! -f "./.env.development" ] || . "./.env.development"
  [ ! -f "./.env.local" ] || . "./.env.local"
  [ ! -f "./.env.development.local" ] || . "./.env.development.local"
  set +a
  load_defaults
}

load_app_env() {
  app_env="${APP_ENV:-development}"
  set -a
  [ ! -f "./.env" ] || . "./.env"
  [ ! -f "./.env.${app_env}" ] || . "./.env.${app_env}"
  [ ! -f "./.env.local" ] || . "./.env.local"
  [ ! -f "./.env.${app_env}.local" ] || . "./.env.${app_env}.local"
  set +a
  load_defaults
}

compose_cmd() {
  docker_bin="${DOCKER}"
  if "$docker_bin" compose version >/dev/null 2>&1; then
    printf '%s compose' "$docker_bin"
  elif command -v docker-compose >/dev/null 2>&1; then
    printf '%s' "docker-compose"
  else
    printf '%s compose' "$docker_bin"
  fi
}

compose_project_args() {
  printf '%s\n%s\n' "-p" "${COMPOSE_PROJECT_NAME:-${APP_NAME}}"
}

compose_file_abs() {
  root="$(repo_root)"
  printf '%s/%s' "$root" "${COMPOSE_FILE}"
}
