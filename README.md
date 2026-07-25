# caddy-rolling-deployment

A [Caddy](https://caddyserver.com) implementation of a **rolling deployment strategy** for Docker containers: a webhook updates a named container to a new image, one host at a time, preserving the container's existing configuration.

- **Module ID:** `http.handlers.rolling_deployment`
- **Caddyfile directive:** `rolling_deployment`
- **Go module:** `github.com/multionlabs/caddy-rolling-deployment`
- **Caddy docs:** [http.handlers.rolling_deployment](https://caddyserver.com/docs/modules/http.handlers.rolling_deployment)

## What it does

When a deploy webhook is received, the plugin:

1. Finds configured Docker hosts where a container with the given **name** is already running.
2. On each of those hosts (in order), pulls the new image, snapshots the running container's spec, renames the old container aside, starts a replacement with the same name/spec and the new image, then removes the backup on success.
3. Stops on the first host failure (fail-fast). Hosts already updated stay on the new image (**HTTP 207**); a failed host is rolled back when possible.

Preserved from the previous container (when present): command/entrypoint, env, labels, mounts (bind + named volumes), published ports, networks, restart policy, and related create options.

## Install

```bash
xcaddy build \
  --with github.com/multionlabs/caddy-rolling-deployment
```

Pin a version:

```bash
xcaddy build \
  --with github.com/multionlabs/caddy-rolling-deployment@v0.9.0
```

Local checkout:

```bash
xcaddy build \
  --with github.com/multionlabs/caddy-rolling-deployment=.
```

## Sample Caddyfile

```caddyfile
deploy.example.com {
	rolling_deployment {
		secret {$ROLLING_DEPLOY_SECRET}
		docker_hosts unix:///var/run/docker.sock
		# docker_hosts tcp://host-a:2375 tcp://host-b:2375
	}
}
```

| Option | Required | Description |
|--------|----------|-------------|
| `secret` | yes | Shared secret; must match the webhook path segment. |
| `docker_hosts` | no | One or more Docker Engine API endpoints. Defaults to `unix:///var/run/docker.sock`. |

Directive order is registered in code (`before respond`).

## Webhook

```text
POST|GET /webhooks/rolling-deployment/{secret}/{service_container}/{service_image}
```

- `service_container` — exact Docker container **name** (must already be running on at least one configured host).
- `service_image` — image reference; may contain `/` (e.g. `ghcr.io/org/app:1.2.3`).

Example (typically called by a CI/CD job after publishing an image):

```bash
curl -si \
  "https://deploy.example.com/webhooks/rolling-deployment/${ROLLING_DEPLOY_SECRET}/api/ghcr.io/acme/api:1.2.3"
```

### Response

JSON body (success / partial):

```json
{
  "partial": false,
  "hosts": [
    { "host_index": 0, "ok": true }
  ]
}
```

Raw Docker host URLs, daemon error text, and the request's container/image strings are **not** echoed in the response (details stay in Caddy logs).

| Status | Meaning |
|--------|---------|
| `200` | All selected hosts updated. |
| `207` | Partial: at least one host updated, then the roll stopped. |
| `400` | Bad path/image, or container not running on any configured host. |
| `401` | Secret mismatch. |
| `409` | A deploy for the same container name is already in progress. |
| `422` | Image/spec incompatible; previous container restored when possible. |
| `502` | Docker/infrastructure failure. |

## Behavior notes

- **Selection**: only hosts where the named container is currently running are updated; others are skipped.
- **Concurrency**: overlapping deploys of the **same** container name return `409`; different names can run in parallel.
- **Rollback**: on a failed recreate/start, the plugin attempts to restore the previous container (backup name pattern: `{name}_rollback_YYYYMMDDHHMMSS`).
- **Secrets**: treat the webhook secret like a deploy credential; prefer HTTPS in production.

## Development

```bash
go test ./...
golangci-lint run ./...
```

Integration scenarios (requires Docker + a test Caddy from `tests/with-caddy/run.sh`):

```bash
./tests/with-caddy/scenario-basic.sh
./tests/with-caddy/scenario-spec-preservation.sh
./tests/with-caddy/scenario-rollback.sh
./tests/with-caddy/scenario-concurrency.sh
```

## Release

```bash
git tag v0.9.0
git push origin v0.9.0
```

## License

MIT — see [LICENSE](./LICENSE).
