#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
DOCKER_BIN="${DOCKER:-docker}"
COMPOSE_FILE="${COMPOSE_FILE:-$ROOT_DIR/deploy/compose/dev-compose.yaml}"
ENV_LOCAL_FILE="${ENV_LOCAL_FILE:-$ROOT_DIR/.env.development.local}"
PAT_CONTAINER_PATH="/zitadel/bootstrap/admin-machine.pat"
STATE_CONTAINER_PATH="/zitadel/bootstrap/dev-bootstrap.json"
MARK_START="# >>> sekai-zitadel-bootstrap >>>"
MARK_END="# <<< sekai-zitadel-bootstrap <<<"
TMP_DIR=""

cleanup() {
	if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
}

require_bin() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "[dev-env-up] missing required command: $1" >&2
		exit 1
	}
}

load_env() {
	set -a
	[ -f "$ROOT_DIR/.env" ] && . "$ROOT_DIR/.env"
	[ -f "$ROOT_DIR/.env.development" ] && . "$ROOT_DIR/.env.development"
	[ -f "$ROOT_DIR/.env.local" ] && . "$ROOT_DIR/.env.local"
	[ -f "$ENV_LOCAL_FILE" ] && . "$ENV_LOCAL_FILE"
	set +a
}

compose_psq() {
	"$DOCKER_BIN" compose -f "$COMPOSE_FILE" ps -q "$1"
}

wait_for_ready() {
	local api_container="$1"
	local attempt

	for attempt in $(seq 1 60); do
		if "$DOCKER_BIN" exec "$api_container" /app/zitadel ready >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done

	echo "[dev-env-up] ZITADEL did not become ready in time" >&2
	exit 1
}

copy_from_container() {
	local container="$1"
	local source_path="$2"
	local target_path="$3"

	"$DOCKER_BIN" cp "$container:$source_path" "$target_path" >/dev/null 2>&1
}

wait_for_container_file() {
	local container="$1"
	local source_path="$2"
	local target_path="$3"
	local attempt

	for attempt in $(seq 1 60); do
		if copy_from_container "$container" "$source_path" "$target_path"; then
			return 0
		fi
		sleep 2
	done

	echo "[dev-env-up] missing bootstrap artifact: $source_path" >&2
	exit 1
}

api_get() {
	local proxy_container="$1"
	local admin_pat="$2"
	local path="$3"

	"$DOCKER_BIN" exec "$proxy_container" \
		wget -qO- \
		--header="Host: ${ZITADEL_DOMAIN:-localhost}:${ZITADEL_PORT:-18081}" \
		--header="Authorization: Bearer ${admin_pat}" \
		"http://127.0.0.1/api${path}"
}

api_post() {
	local proxy_container="$1"
	local admin_pat="$2"
	local path="$3"
	local payload="$4"

	"$DOCKER_BIN" exec "$proxy_container" \
		wget -qO- \
		--header="Host: ${ZITADEL_DOMAIN:-localhost}:${ZITADEL_PORT:-18081}" \
		--header="Authorization: Bearer ${admin_pat}" \
		--header="Content-Type: application/json" \
		--post-data="$payload" \
		"http://127.0.0.1/api${path}"
}

org_slug() {
	printf '%s' "${ZITADEL_FIRSTINSTANCE_ORG_NAME:-sekai}" \
		| tr '[:upper:]' '[:lower:]' \
		| sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'
}

update_env_local() {
	local target_file="$1"
	local block_file="$2"
	local stripped_file="$3"
	local merged_file="$4"

	if [ -f "$target_file" ]; then
		awk -v start="$MARK_START" -v end="$MARK_END" '
			$0 == start { skip = 1; next }
			$0 == end { skip = 0; next }
			skip != 1 { print }
		' "$target_file" >"$stripped_file"
	else
		: >"$stripped_file"
	fi

	cp "$stripped_file" "$merged_file"
	if [ -s "$merged_file" ]; then
		printf '\n' >>"$merged_file"
	fi
	cat "$block_file" >>"$merged_file"
	mv "$merged_file" "$target_file"
}

main() {
	require_bin jq

	load_env

	local api_container proxy_container
	api_container="$(compose_psq zitadel-api)"
	proxy_container="$(compose_psq zitadel-proxy)"

	if [ -z "$api_container" ] || [ -z "$proxy_container" ]; then
		echo "[dev-env-up] ZITADEL containers are not running" >&2
		exit 1
	fi

	wait_for_ready "$api_container"

	TMP_DIR="$(mktemp -d)"
	trap cleanup EXIT

	local pat_file state_file admin_pat
	pat_file="$TMP_DIR/admin-machine.pat"
	state_file="$TMP_DIR/dev-bootstrap.json"

	wait_for_container_file "$api_container" "$PAT_CONTAINER_PATH" "$pat_file"
	admin_pat="$(tr -d '\r\n' <"$pat_file")"

	local org_json org_id project_name app_name redirect_url logout_url
	local attempt
	for attempt in $(seq 1 30); do
		if org_json="$(api_get "$proxy_container" "$admin_pat" "/management/v1/orgs/me" 2>/dev/null)"; then
			break
		fi
		sleep 2
	done
	if [ -z "${org_json:-}" ]; then
		echo "[dev-env-up] ZITADEL management API did not become reachable in time" >&2
		exit 1
	fi
	org_id="$(printf '%s' "$org_json" | jq -r '.org.id')"
	project_name="${ZITADEL_BOOTSTRAP_PROJECT_NAME:-sekai-local}"
	app_name="${ZITADEL_BOOTSTRAP_APP_NAME:-sekai-admin-web}"
	redirect_url="${ZITADEL_REDIRECT_URL:-http://localhost:${APP_PORT:-8080}/api/v1/admin/login/callback}"
	logout_url="${ZITADEL_BOOTSTRAP_LOGOUT_URL:-http://localhost:${APP_PORT:-8080}/admin/login}"

	if ! copy_from_container "$api_container" "$STATE_CONTAINER_PATH" "$state_file" \
		|| ! jq -e '.project_id and .client_id' "$state_file" >/dev/null 2>&1; then
		local project_payload project_json project_id
		project_payload="$(jq -cn --arg name "$project_name" '{name: $name}')"
		project_json="$(api_post "$proxy_container" "$admin_pat" "/management/v1/projects" "$project_payload")"
		project_id="$(printf '%s' "$project_json" | jq -r '.id')"

		local app_payload app_json client_id app_id
		app_payload="$(jq -cn \
			--arg name "$app_name" \
			--arg redirect "$redirect_url" \
			--arg logout "$logout_url" \
			'{
				name: $name,
				redirectUris: [$redirect],
				postLogoutRedirectUris: [$logout],
				appType: "OIDC_APP_TYPE_WEB",
				authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE"
			}')"
		app_json="$(api_post "$proxy_container" "$admin_pat" "/management/v1/projects/${project_id}/apps/oidc" "$app_payload")"
		client_id="$(printf '%s' "$app_json" | jq -r '.clientId')"
		app_id="$(printf '%s' "$app_json" | jq -r '.appId')"

		jq -cn \
			--arg org_id "$org_id" \
			--arg project_id "$project_id" \
			--arg project_name "$project_name" \
			--arg app_id "$app_id" \
			--arg client_id "$client_id" \
			--arg app_name "$app_name" \
			--arg redirect_url "$redirect_url" \
			--arg logout_url "$logout_url" \
			'{
				org_id: $org_id,
				project_id: $project_id,
				project_name: $project_name,
				app_id: $app_id,
				client_id: $client_id,
				app_name: $app_name,
				redirect_url: $redirect_url,
				logout_url: $logout_url
			}' >"$state_file"
		"$DOCKER_BIN" cp "$state_file" "$api_container:$STATE_CONTAINER_PATH" >/dev/null
	fi

	local project_id client_id audience_scope login_name login_password
	project_id="$(jq -r '.project_id' "$state_file")"
	client_id="$(jq -r '.client_id' "$state_file")"
	audience_scope="urn:zitadel:iam:org:project:id:${project_id}:aud"
	login_name="${ZITADEL_FIRSTINSTANCE_ORG_HUMAN_USERNAME:-admin}@$(org_slug).${ZITADEL_DOMAIN:-localhost}"
	login_password="${ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORD:-Admin123!Admin123!}"

	local block_file stripped_file merged_file
	block_file="$TMP_DIR/bootstrap-env.block"
	stripped_file="$TMP_DIR/bootstrap-env.stripped"
	merged_file="$TMP_DIR/bootstrap-env.merged"

	cat >"$block_file" <<EOF
$MARK_START
# generated by scripts/bootstrap-zitadel-dev.sh
# local zitadel login: ${login_name}
# local zitadel password: ${login_password}
ZITADEL_AUDIENCE=${project_id}
ZITADEL_CLIENT_ID=${client_id}
ZITADEL_SCOPES=openid,profile,email,${audience_scope}
ZITADEL_PRIVATE_KEY_PATH=
ZITADEL_PRIVATE_KEY_ID=
$MARK_END
EOF

	update_env_local "$ENV_LOCAL_FILE" "$block_file" "$stripped_file" "$merged_file"

	echo "[dev-env-up] local ZITADEL ready"
	echo "[dev-env-up] login: ${login_name}"
	echo "[dev-env-up] password: ${login_password}"
	echo "[dev-env-up] project: ${project_id}"
	echo "[dev-env-up] client: ${client_id}"
	echo "[dev-env-up] env overrides: ${ENV_LOCAL_FILE}"
}

main "$@"
