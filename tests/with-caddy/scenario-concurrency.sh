#!/usr/bin/env bash
# Concurrency: overlapping deploys for the same container must yield HTTP 409
# for the loser (ErrInProgress), while the winner completes successfully.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_common.sh
source "${DIR}/_common.sh"

FROM_IMAGE="${FROM_IMAGE:-nginx:1.27.3-alpine}"
# Target image is removed locally first so the winning deploy spends time in
# Pull while holding the in-flight lock — giving concurrent requests a window
# to observe 409 Conflict.
TO_IMAGE="${TO_IMAGE:-nginx:1.27.4-alpine}"
STATUS1_FILE="$(mktemp)"
PIDS=()

require_cmds curl docker
require_caddy

cleanup() {
	local pid
	for pid in "${PIDS[@]:-}"; do
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	done
	rm -f "$STATUS1_FILE"
	cleanup_container
}
trap cleanup EXIT

http_code() {
	local image="$1"
	curl -s -o /dev/null -w '%{http_code}' "${WEBHOOK_PREFIX}/${CONTAINER_NAME}/${image}"
}

log "scenario: concurrent deploy of the same container"
cleanup_container

log "starting ${CONTAINER_NAME} from ${FROM_IMAGE}"
docker pull "$FROM_IMAGE" >/dev/null
docker run -d --name "$CONTAINER_NAME" "$FROM_IMAGE" >/dev/null
assert_eq "$(container_image)" "$FROM_IMAGE" "initial image"
show_container

log "removing local ${TO_IMAGE} to widen the in-flight pull window"
docker rmi "$TO_IMAGE" >/dev/null 2>&1 || true

log "starting first deploy in background -> ${TO_IMAGE}"
(
	set +e
	code="$(http_code "$TO_IMAGE")"
	printf '%s' "$code" >"$STATUS1_FILE"
) &
PIDS+=($!)

# Poll with a second deploy until we see 409, or the first deploy finishes.
log "probing for HTTP 409 from overlapping deploy"
saw_409=0
for _ in $(seq 1 100); do
	if ! kill -0 "${PIDS[0]}" 2>/dev/null; then
		break
	fi
	code="$(http_code "$TO_IMAGE")"
	log "overlap probe -> HTTP ${code}"
	if [[ "$code" == "409" ]]; then
		saw_409=1
		break
	fi
	sleep 0.05
done

log "waiting for background deploy to finish"
wait "${PIDS[0]}" || true
PIDS=()
status1="$(cat "$STATUS1_FILE")"
log "first deploy -> HTTP ${status1}"

assert_eq "$status1" "200" "winning deploy status"
if [[ "$saw_409" != "1" ]]; then
	die "never observed HTTP 409 from a concurrent deploy (first may have finished too quickly)"
fi
pass "observed HTTP 409 while another deploy was in progress"

assert_eq "$(container_image)" "$TO_IMAGE" "image after winning deploy"
container_running || die "${CONTAINER_NAME} is not running after deploy"
show_container

# Lock must be released: a follow-up deploy of the same container should work.
# Stay on TO_IMAGE (no-op roll) — still exercises acquire/release.
deploy_expect_status "$TO_IMAGE" "200"
container_running || die "${CONTAINER_NAME} is not running after follow-up deploy"

pass "concurrency scenario succeeded"
