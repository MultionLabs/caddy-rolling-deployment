#!/usr/bin/env bash
# Basic rolling deploy: alpine:3.19 -> alpine:3.20, verify image swap.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${DIR}/_common.sh"

FROM_IMAGE="${FROM_IMAGE:-alpine:3.19}"
TO_IMAGE="${TO_IMAGE:-alpine:3.20}"

require_cmds curl docker
require_caddy

cleanup() {
	cleanup_container
}
trap cleanup EXIT

log "scenario: basic rolling deploy (${FROM_IMAGE} -> ${TO_IMAGE})"
cleanup_container

log "starting ${CONTAINER_NAME} from ${FROM_IMAGE}"
docker pull "$FROM_IMAGE" >/dev/null
docker run -d --name "$CONTAINER_NAME" "$FROM_IMAGE" sleep infinity >/dev/null
assert_eq "$(container_image)" "$FROM_IMAGE" "initial image"
show_container

deploy_expect_status "$TO_IMAGE" "200"

assert_eq "$(container_image)" "$TO_IMAGE" "image after deploy"
container_running || die "${CONTAINER_NAME} is not running after deploy"
show_container

pass "basic rolling deploy scenario succeeded"
