#!/usr/bin/env bash
# Shared helpers for with-caddy scenario scripts.
# shellcheck disable=SC2034

set -euo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PORT="${PORT:-8080}"
SECRET="${ROLLING_DEPLOY_SECRET:-test-secret}"
BASE_URL="${BASE_URL:-http://127.0.0.1:${PORT}}"
WEBHOOK_PREFIX="${BASE_URL}/webhooks/rolling-deployment/${SECRET}"

PREFIX="rolling-caddy-test"
CONTAINER_NAME="${CONTAINER_NAME:-${PREFIX}-svc}"

die() {
	echo "error: $*" >&2
	exit 1
}

log() {
	echo "==> $*"
}

pass() {
	echo "OK: $*"
}

require_cmds() {
	local cmd
	for cmd in "$@"; do
		command -v "$cmd" >/dev/null 2>&1 || die "required command not found: ${cmd}"
	done
}

require_caddy() {
	local body
	body="$(curl -sf "${BASE_URL}/" 2>/dev/null || true)"
	if [[ "$body" != *rolling_deployment* ]]; then
		die "test Caddy is not reachable at ${BASE_URL}/ — start it with: ${SCENARIO_DIR}/run.sh"
	fi
}

cleanup_container() {
	local name="${1:-$CONTAINER_NAME}"
	docker rm -f "$name" >/dev/null 2>&1 || true
	# Leftover rollback backups from interrupted runs.
	local backup
	while read -r backup; do
		[[ -z "$backup" ]] && continue
		docker rm -f "$backup" >/dev/null 2>&1 || true
	done < <(docker ps -aq --filter "name=${name}_rollback_" 2>/dev/null || true)
}

container_image() {
	local name="${1:-$CONTAINER_NAME}"
	docker inspect -f '{{.Config.Image}}' "$name"
}

container_running() {
	local name="${1:-$CONTAINER_NAME}"
	[[ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || echo false)" == "true" ]]
}

assert_eq() {
	local got="$1" want="$2" msg="${3:-values differ}"
	if [[ "$got" != "$want" ]]; then
		die "${msg}: got ${got@Q}, want ${want@Q}"
	fi
}

assert_contains() {
	local haystack="$1" needle="$2" msg="${3:-missing expected substring}"
	if [[ "$haystack" != *"$needle"* ]]; then
		die "${msg}: expected to find ${needle@Q} in ${haystack@Q}"
	fi
}

assert_http_status() {
	local url="$1" want="$2"
	local got
	got="$(curl -s -o /tmp/rolling-caddy-body.txt -w '%{http_code}' "$url")"
	if [[ "$got" != "$want" ]]; then
		die "HTTP ${url}: got ${got}, want ${want}; body=$(cat /tmp/rolling-caddy-body.txt)"
	fi
	pass "HTTP ${want} from ${url}"
}

deploy() {
	local image="$1"
	local url="${WEBHOOK_PREFIX}/${CONTAINER_NAME}/${image}"
	log "deploy ${CONTAINER_NAME} -> ${image}"
	curl -si "$url"
	echo
}

deploy_expect_status() {
	local image="$1" want="$2"
	local url="${WEBHOOK_PREFIX}/${CONTAINER_NAME}/${image}"
	log "deploy ${CONTAINER_NAME} -> ${image} (expect HTTP ${want})"
	assert_http_status "$url" "$want"
}

show_container() {
	local name="${1:-$CONTAINER_NAME}"
	docker ps -a --filter "name=^/${name}$" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
}
