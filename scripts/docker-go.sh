#!/bin/sh

set -eu

WORKSPACE_DIR="${WORKSPACE_DIR:-$(pwd)}"
DOCKER_BIN="${DOCKER_BIN:-docker}"
GO_DOCKER_IMAGE="${GO_DOCKER_IMAGE:-golang:1.26.2-alpine3.23}"
GO_DOCKER_WORKDIR="${GO_DOCKER_WORKDIR:-/src}"
GO_DOCKER_MOD_CACHE_VOLUME="${GO_DOCKER_MOD_CACHE_VOLUME:-sekai-master-api-go-mod-cache}"
GO_DOCKER_BUILD_CACHE_VOLUME="${GO_DOCKER_BUILD_CACHE_VOLUME:-sekai-master-api-go-build-cache}"
GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME="${GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME:-sekai-master-api-go-toolchain-cache}"

if [ "$#" -eq 0 ]; then
	echo "usage: $0 <go command...>" >&2
	exit 1
fi

"${DOCKER_BIN}" volume create "${GO_DOCKER_MOD_CACHE_VOLUME}" >/dev/null
"${DOCKER_BIN}" volume create "${GO_DOCKER_BUILD_CACHE_VOLUME}" >/dev/null
"${DOCKER_BIN}" volume create "${GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME}" >/dev/null

exec "${DOCKER_BIN}" run --rm \
	-v "${WORKSPACE_DIR}:${GO_DOCKER_WORKDIR}" \
	-v "${GO_DOCKER_MOD_CACHE_VOLUME}:/go/pkg/mod" \
	-v "${GO_DOCKER_BUILD_CACHE_VOLUME}:/root/.cache/go-build" \
	-v "${GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME}:/root/.cache/go-toolchains" \
	-w "${GO_DOCKER_WORKDIR}" \
	-e GOMODCACHE=/go/pkg/mod \
	-e GOCACHE=/root/.cache/go-build \
	-e GOTOOLCHAIN=auto \
	-e GOTOOLCHAINCACHE=/root/.cache/go-toolchains \
	"${GO_DOCKER_IMAGE}" \
	sh -lc 'export PATH=/usr/local/go/bin:$PATH && apk add --no-cache git >/dev/null && exec "$@"' sh "$@"
