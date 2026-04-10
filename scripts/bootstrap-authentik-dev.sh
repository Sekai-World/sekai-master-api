#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/deploy/compose/dev-compose.yaml}"
AUTHENTIK_DOCKER_NETWORK="${AUTHENTIK_DOCKER_NETWORK:-sekai-dev}"
AUTHENTIK_CONTAINER_URL="${AUTHENTIK_CONTAINER_URL:-http://authentik-server:9000}"
CURL_PROBE_IMAGE="${CURL_PROBE_IMAGE:-curlimages/curl:8.13.0}"

load_env() {
	set -a
	[ -f "$ROOT_DIR/.env" ] && . "$ROOT_DIR/.env"
	[ -f "$ROOT_DIR/.env.development" ] && . "$ROOT_DIR/.env.development"
	[ -f "$ROOT_DIR/.env.local" ] && . "$ROOT_DIR/.env.local"
	[ -f "$ROOT_DIR/.env.development.local" ] && . "$ROOT_DIR/.env.development.local"
	set +a
}

resolve_compose_cmd() {
	if docker compose version >/dev/null 2>&1; then
		echo "docker compose"
	elif command -v docker-compose >/dev/null 2>&1; then
		echo "docker-compose"
	else
		echo "docker compose"
	fi
}

build_probe_bases() {
	local authentik_port="$1"
	local oidc_internal_url="${OIDC_INTERNAL_URL:-}"
	local -a candidates
	local candidate
	local internal_host

	candidates=(
		"http://localhost:${authentik_port}"
		"http://host.docker.internal:${authentik_port}"
		"${AUTHENTIK_CONTAINER_URL%/}"
	)

	if [ -n "$oidc_internal_url" ]; then
		internal_host="${oidc_internal_url%/}"
		internal_host="${internal_host%%/application/o/*}"
		candidates+=("$internal_host")
	fi

	for candidate in "${candidates[@]}"; do
		[ -n "$candidate" ] || continue
		printf '%s\n' "$candidate"
	done
}

probe_url() {
	local url="$1"
	local host
	local without_scheme

	if curl -fsS --max-time 5 "$url" >/dev/null 2>&1; then
		return 0
	fi

	without_scheme="${url#*://}"
	host="${without_scheme%%[:/]*}"

	if [ "$host" != "authentik-server" ]; then
		return 1
	fi

	docker run --rm --network "$AUTHENTIK_DOCKER_NETWORK" "$CURL_PROBE_IMAGE" \
		-fsS --max-time 5 "$url" >/dev/null 2>&1
}

wait_for_authentik_base() {
	local authentik_port="$1"
	local attempt
	local base_url

	for attempt in $(seq 1 120); do
		while IFS= read -r base_url; do
			if probe_url "${base_url}/-/health/ready/"; then
				printf '%s\n' "$base_url"
				return 0
			fi
		done < <(build_probe_bases "$authentik_port")
		sleep 2
	done

	echo "[dev-env-up] authentik health endpoint did not become ready in time" >&2
	exit 1
}

wait_for_url() {
	local url="$1"
	local label="$2"
	local attempt

	for attempt in $(seq 1 120); do
		if probe_url "$url"; then
			return 0
		fi
		sleep 2
	done

	echo "[dev-env-up] ${label} did not become ready in time" >&2
	exit 1
}

apply_local_blueprint() {
	local compose_cmd="$1"
	local blueprint_src="$ROOT_DIR/deploy/authentik/blueprints/sekai-local.yaml"
	local blueprint_dst="/blueprints/sekai-local.yaml"
	local log_file
	local attempt

	if [ ! -f "$blueprint_src" ]; then
		echo "[dev-env-up] local authentik blueprint not found: $blueprint_src" >&2
		exit 1
	fi

	log_file="$(mktemp)"
	for attempt in $(seq 1 30); do
		if $compose_cmd -f "$COMPOSE_FILE" exec -T authentik-worker sh -lc \
			"cat > '$blueprint_dst' && /ak-root/.venv/bin/python manage.py apply_blueprint '$blueprint_dst'" \
			<"$blueprint_src" >"$log_file" 2>&1; then
			rm -f "$log_file"
			return 0
		fi
		sleep 2
	done

	cat "$log_file" >&2
	rm -f "$log_file"
	echo "[dev-env-up] failed to apply local authentik blueprint" >&2
	exit 1
}

main() {
	load_env

	local compose_cmd authentik_port app_slug login_user login_password
	local authentik_base issuer_url probe_issuer_url
	local display_authentik_url display_issuer_url

	compose_cmd="$(resolve_compose_cmd)"
	authentik_port="${AUTHENTIK_PORT:-19100}"
	app_slug="${AUTHENTIK_LOCAL_APP_SLUG:-sekai-admin-web}"
	login_user="${AUTHENTIK_LOCAL_TEST_USERNAME:-admin}"
	login_password="${AUTHENTIK_LOCAL_TEST_PASSWORD:-Admin123!Admin123!}"
	display_authentik_url="http://localhost:${authentik_port}"
	display_issuer_url="${OIDC_ISSUER_URL:-${display_authentik_url}/application/o/${app_slug}/}"

	authentik_base="$(wait_for_authentik_base "$authentik_port")"
	apply_local_blueprint "$compose_cmd"
	issuer_url="${authentik_base}/application/o/${app_slug}/"
	probe_issuer_url="${issuer_url}.well-known/openid-configuration"
	wait_for_url "$probe_issuer_url" "authentik oidc discovery"

	echo "[dev-env-up] local authentik ready"
	echo "[dev-env-up] authentik url: ${display_authentik_url}"
	echo "[dev-env-up] oidc issuer: ${display_issuer_url}"
	if [ "$authentik_base" != "$display_authentik_url" ]; then
		echo "[dev-env-up] bootstrap probe url: ${authentik_base}"
	fi
	echo "[dev-env-up] local oidc test user: ${login_user}"
	echo "[dev-env-up] local oidc test password: ${login_password}"
	echo "[dev-env-up] bootstrap admin user: akadmin"
	echo "[dev-env-up] bootstrap admin password: ${AUTHENTIK_BOOTSTRAP_PASSWORD:-Admin123!Admin123!}"
}

main "$@"
