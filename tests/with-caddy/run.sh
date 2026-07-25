#!/usr/bin/env bash
# Build a Caddy binary with the local rolling_deployment plugin and run a test instance.
set -euo pipefail

LOCAL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${LOCAL_DIR}/.bin"
CADDY_BIN="${BIN_DIR}/caddy"
CADDYFILE="${LOCAL_DIR}/Caddyfile"

# Plugin root = directory that owns go.mod (works from tests/with-caddy or elsewhere).
PLUGIN_DIR=""
_dir="$LOCAL_DIR"
while [[ "$_dir" != "/" ]]; do
	if [[ -f "$_dir/go.mod" ]]; then
		PLUGIN_DIR="$_dir"
		break
	fi
	_dir="$(cd "$_dir/.." && pwd)"
done
if [[ -z "$PLUGIN_DIR" ]]; then
	echo "error: could not find go.mod above ${LOCAL_DIR}" >&2
	exit 1
fi
GO_MOD="${PLUGIN_DIR}/go.mod"

PORT="${PORT:-8080}"
SECRET="${ROLLING_DEPLOY_SECRET:-test-secret}"
SKIP_BUILD="${SKIP_BUILD:-0}"

die() {
	echo "error: $*" >&2
	exit 1
}

# Module path from local go.mod only — this plugin is not published yet.
# xcaddy --with path=dir replaces that import with this checkout (no remote fetch).
module_path() {
	local path
	path="$(awk '/^module / { print $2; exit }' "$GO_MOD" 2>/dev/null || true)"
	if [[ -z "$path" ]]; then
		die "could not read module path from ${GO_MOD}"
	fi
	printf '%s\n' "$path"
}

resolve_docker_host() {
	if [[ -n "${DOCKER_HOST:-}" ]]; then
		printf '%s\n' "$DOCKER_HOST"
		return
	fi

	local sock
	for sock in \
		"${HOME}/.docker/run/docker.sock" \
		"${HOME}/.docker/desktop/docker.sock" \
		"/var/run/docker.sock"; do
		if [[ -S "$sock" ]]; then
			printf 'unix://%s\n' "$sock"
			return
		fi
	done

	die "no Docker socket found; set DOCKER_HOST (e.g. unix:///var/run/docker.sock)"
}

need_xcaddy() {
	if command -v xcaddy >/dev/null 2>&1; then
		return
	fi
	die "xcaddy not found. Install with: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest"
}

build_caddy() {
	need_xcaddy
	mkdir -p "$BIN_DIR"

	local module
	module="$(module_path)"

	echo "Building Caddy with local plugin:"
	echo "  module:  ${module}"
	echo "  replace: ${PLUGIN_DIR}"
	# Explicit local replace: never depends on a published module at module path.
	xcaddy build \
		--output "$CADDY_BIN" \
		--with "${module}=${PLUGIN_DIR}"
	echo "Built ${CADDY_BIN}"
}

export DOCKER_HOST
DOCKER_HOST="$(resolve_docker_host)"
export ROLLING_DEPLOY_SECRET="$SECRET"

if [[ ! -f "$CADDYFILE" ]]; then
	die "missing Caddyfile at ${CADDYFILE}"
fi

if [[ "$SKIP_BUILD" != "1" ]] || [[ ! -x "$CADDY_BIN" ]]; then
	build_caddy
fi

# Rewrite listen port without mutating the checked-in Caddyfile.
TMP_CADDYFILE="$(mktemp)"
trap 'rm -f "$TMP_CADDYFILE"' EXIT
sed "s/:8080/:${PORT}/" "$CADDYFILE" >"$TMP_CADDYFILE"

echo
echo "Docker host:  ${DOCKER_HOST}"
echo "Secret:       ${SECRET}"
echo "Listen:       http://127.0.0.1:${PORT}"
echo "Admin API:    http://localhost:2019"
echo
echo "Try:"
echo "  curl -s http://127.0.0.1:${PORT}/"
echo "  ./tests/with-caddy/scenario-basic.sh"
echo "  ./tests/with-caddy/scenario-spec-preservation.sh"
echo "  ./tests/with-caddy/scenario-rollback.sh"
echo "  ./tests/with-caddy/scenario-concurrency.sh"
echo
echo "Skip rebuild next time with: SKIP_BUILD=1 $0"
echo
exec "$CADDY_BIN" run --config "$TMP_CADDYFILE" --adapter caddyfile
