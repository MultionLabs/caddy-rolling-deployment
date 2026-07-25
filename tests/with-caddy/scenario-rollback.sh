#!/usr/bin/env bash
# Rollback: deploy an image that cannot start with the preserved nginx
# entrypoint/cmd, expect HTTP 422 and the original container restored.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${DIR}/_common.sh"

FROM_IMAGE="${FROM_IMAGE:-nginx:1.27.3-alpine}"
# Alpine lacks nginx's /docker-entrypoint.sh, so Run/Start should fail and
# trigger rollback to the previous container.
BAD_IMAGE="${BAD_IMAGE:-alpine:3.20}"

require_cmds curl docker
require_caddy

cleanup() {
	cleanup_container
}
trap cleanup EXIT

log "scenario: failed deploy rolls back (${FROM_IMAGE} -x ${BAD_IMAGE})"
cleanup_container

log "starting ${CONTAINER_NAME} from ${FROM_IMAGE}"
docker pull "$FROM_IMAGE" >/dev/null
docker pull "$BAD_IMAGE" >/dev/null
docker run -d \
	--name "$CONTAINER_NAME" \
	--label "rolling.scenario=rollback" \
	-e "ROLLING_ENV=should-survive" \
	"$FROM_IMAGE" >/dev/null

BEFORE_ID="$(docker inspect -f '{{.Id}}' "$CONTAINER_NAME")"
BEFORE_IMAGE="$(container_image)"
assert_eq "$BEFORE_IMAGE" "$FROM_IMAGE" "initial image"
container_running || die "container not running before deploy"
show_container

log "attempting deploy that should fail and roll back"
deploy_expect_status "$BAD_IMAGE" "422"

container_running || die "${CONTAINER_NAME} is not running after rollback"
AFTER_IMAGE="$(container_image)"
AFTER_ID="$(docker inspect -f '{{.Id}}' "$CONTAINER_NAME")"
AFTER_ENV="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$CONTAINER_NAME")"
AFTER_LABELS="$(docker inspect -f '{{json .Config.Labels}}' "$CONTAINER_NAME")"

assert_eq "$AFTER_IMAGE" "$FROM_IMAGE" "image after rollback"
assert_eq "$AFTER_ID" "$BEFORE_ID" "container id after rollback (same instance restored)"
assert_contains "$AFTER_ENV" "ROLLING_ENV=should-survive" "env preserved via rollback"
assert_contains "$AFTER_LABELS" '"rolling.scenario":"rollback"' "label preserved via rollback"

# No live replacement should remain under the service name with the bad image.
if [[ "$AFTER_IMAGE" == "$BAD_IMAGE" ]]; then
	die "service unexpectedly running bad image after rollback"
fi

# Backup leftovers should not still be running under the original name.
show_container
pass "rollback scenario succeeded"
