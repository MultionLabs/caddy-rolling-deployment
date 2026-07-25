#!/usr/bin/env bash
# Spec preservation: custom network, published port, named + bind volumes,
# labels, env, restart policy, and network alias must survive recreation.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${DIR}/_common.sh"

FROM_IMAGE="${FROM_IMAGE:-nginx:1.27.3-alpine}"
TO_IMAGE="${TO_IMAGE:-nginx:1.27.4-alpine}"
NETWORK_NAME="${PREFIX}-net"
VOLUME_NAME="${PREFIX}-vol"
HOST_PORT="${HOST_PORT:-18080}"
BIND_DIR=""

require_cmds curl docker
require_caddy

cleanup() {
	cleanup_container
	docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
	docker volume rm "$VOLUME_NAME" >/dev/null 2>&1 || true
	if [[ -n "$BIND_DIR" && -d "$BIND_DIR" ]]; then
		rm -rf "$BIND_DIR"
	fi
}
trap cleanup EXIT

log "scenario: spec preservation (${FROM_IMAGE} -> ${TO_IMAGE})"
cleanup

# Keep bind data under the repo path so Docker Desktop file sharing can see it.
BIND_ROOT="${SCENARIO_DIR}/.bind-data"
mkdir -p "$BIND_ROOT"
BIND_DIR="$(mktemp -d "${BIND_ROOT}/${PREFIX}-bind.XXXXXX")"
echo "host-bind-marker" >"${BIND_DIR}/marker.txt"

log "creating network ${NETWORK_NAME} and volume ${VOLUME_NAME}"
docker network create "$NETWORK_NAME" >/dev/null
docker volume create "$VOLUME_NAME" >/dev/null

log "starting ${CONTAINER_NAME} with rich spec"
docker pull "$FROM_IMAGE" >/dev/null
docker run -d \
	--name "$CONTAINER_NAME" \
	--network "$NETWORK_NAME" \
	--network-alias "rolling-svc" \
	--restart unless-stopped \
	-p "${HOST_PORT}:80" \
	-v "${VOLUME_NAME}:/data" \
	-v "${BIND_DIR}:/hostdata:ro" \
	-e "ROLLING_ENV=preserved" \
	--label "rolling.test=1" \
	--label "rolling.scenario=spec-preservation" \
	"$FROM_IMAGE" >/dev/null

assert_eq "$(container_image)" "$FROM_IMAGE" "initial image"
assert_eq "$(docker exec "$CONTAINER_NAME" cat /hostdata/marker.txt)" "host-bind-marker" "bind mount content before deploy"
show_container

# Capture pre-deploy facts for comparison.
BEFORE_LABELS="$(docker inspect -f '{{json .Config.Labels}}' "$CONTAINER_NAME")"
BEFORE_ENV="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER_NAME")"
BEFORE_RESTART="$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$CONTAINER_NAME")"
BEFORE_BINDS="$(docker inspect -f '{{json .HostConfig.Binds}}' "$CONTAINER_NAME")"
BEFORE_MOUNTS="$(docker inspect -f '{{range .Mounts}}{{.Type}}|{{.Name}}|{{.Source}}|{{.Destination}}|{{.RW}} {{end}}' "$CONTAINER_NAME")"
BEFORE_NETWORKS="$(docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}={{json $v.Aliases}} {{end}}' "$CONTAINER_NAME")"
BEFORE_PORTS="$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$CONTAINER_NAME")"

log "pre-deploy labels:   ${BEFORE_LABELS}"
log "pre-deploy env:      $(echo "$BEFORE_ENV" | tr '\n' ' ')"
log "pre-deploy restart:  ${BEFORE_RESTART}"
log "pre-deploy binds:    ${BEFORE_BINDS}"
log "pre-deploy mounts:   ${BEFORE_MOUNTS}"
log "pre-deploy networks: ${BEFORE_NETWORKS}"
log "pre-deploy ports:    ${BEFORE_PORTS}"

deploy_expect_status "$TO_IMAGE" "200"

assert_eq "$(container_image)" "$TO_IMAGE" "image after deploy"
container_running || die "${CONTAINER_NAME} is not running after deploy"
show_container

AFTER_LABELS="$(docker inspect -f '{{json .Config.Labels}}' "$CONTAINER_NAME")"
AFTER_ENV="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER_NAME")"
AFTER_RESTART="$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$CONTAINER_NAME")"
AFTER_BINDS="$(docker inspect -f '{{json .HostConfig.Binds}}' "$CONTAINER_NAME")"
AFTER_MOUNTS="$(docker inspect -f '{{range .Mounts}}{{.Type}}|{{.Name}}|{{.Source}}|{{.Destination}}|{{.RW}} {{end}}' "$CONTAINER_NAME")"
AFTER_NETWORKS="$(docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}={{json $v.Aliases}} {{end}}' "$CONTAINER_NAME")"
AFTER_PORTS="$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$CONTAINER_NAME")"

assert_contains "$AFTER_LABELS" '"rolling.test":"1"' "label rolling.test"
assert_contains "$AFTER_LABELS" '"rolling.scenario":"spec-preservation"' "label rolling.scenario"
assert_contains "$AFTER_ENV" "ROLLING_ENV=preserved" "env ROLLING_ENV"
assert_eq "$AFTER_RESTART" "unless-stopped" "restart policy"

assert_contains "$AFTER_BINDS" "${VOLUME_NAME}:/data" "named volume bind"
assert_contains "$AFTER_BINDS" "$(basename "$BIND_DIR"):/hostdata:ro" "host bind mount"

assert_contains "$AFTER_MOUNTS" "volume|${VOLUME_NAME}|" "named volume mount name"
assert_contains "$AFTER_MOUNTS" "|/data|true" "named volume mount target"
assert_contains "$AFTER_MOUNTS" "bind||" "host bind mount type"
assert_contains "$AFTER_MOUNTS" "$(basename "$BIND_DIR")|/hostdata|false" "host bind mount target (ro)"

assert_contains "$AFTER_NETWORKS" "${NETWORK_NAME}=" "custom network membership"
assert_contains "$AFTER_NETWORKS" "rolling-svc" "network alias"

assert_contains "$AFTER_PORTS" '"80/tcp"' "published container port 80"
assert_contains "$AFTER_PORTS" "\"${HOST_PORT}\"" "host port ${HOST_PORT}"

# Live checks: bind content + HTTP via published port.
assert_eq "$(docker exec "$CONTAINER_NAME" cat /hostdata/marker.txt)" "host-bind-marker" "bind mount content"
curl -sf "http://127.0.0.1:${HOST_PORT}/" >/dev/null || die "published port ${HOST_PORT} not serving after deploy"

pass "spec preservation scenario succeeded"
