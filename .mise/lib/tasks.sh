#!/usr/bin/env sh

set -eu

load_defaults() {
  APP_NAME="${APP_NAME:-sekai-master-api}"
  APP_PORT="${APP_PORT:-18080}"
  COMPOSE_HOST="${COMPOSE_HOST:-host.docker.internal}"
  DOCKER="${DOCKER:-docker}"
  APP_ENV="${APP_ENV:-development}"
  GO_DOCKER_IMAGE="${GO_DOCKER_IMAGE:-golang:1.27.1-alpine3.23}"
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
