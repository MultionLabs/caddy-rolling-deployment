package rolling_deployment

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/multionlabs/caddy-rolling-deployment/deploy"
	"github.com/multionlabs/caddy-rolling-deployment/docker"
	"go.uber.org/zap"
)

const webhookPrefix = "/webhooks/rolling-deployment/"

// Middleware is an HTTP handler that implements a rolling deployment strategy
// for Docker containers. It exposes a deploy webhook and leaves all other
// requests to the next handler in the chain.
//
// ### Caddyfile example
//
// Enable the webhook on `deploy.example.com` (HTTPS via Caddy's automatic
// certificates). Caddy must be able to reach each Docker Engine API endpoint
// listed in `docker_hosts`:
//
// ```
// deploy.example.com {
//     rolling_deployment {
//         secret {$ROLLING_DEPLOY_SECRET}
//         docker_hosts unix:///var/run/docker.sock tcp://docker-b.internal:2375
//     }
// }
// ```
//
// ### Who triggers a deploy, and how
//
// Typically a CI/CD job (GitHub Actions, GitLab CI, etc.) or an operator calls
// the webhook after publishing a new image. Any HTTP method is accepted; `GET`
// or `POST` are common:
//
// ```
// curl -si \
//   "https://deploy.example.com/webhooks/rolling-deployment/${ROLLING_DEPLOY_SECRET}/api/ghcr.io/acme/api:1.2.3"
// ```
//
// Path shape:
//
// `/webhooks/rolling-deployment/{secret}/{container}/{image}`
//
// - `{secret}` must match the configured `secret` (constant-time compared).
// - `{container}` is the exact Docker **container name** that must already be
//   running (e.g. `api`).
// - `{image}` is the new image reference and may contain `/`
//   (e.g. `ghcr.io/acme/api:1.2.3`).
//
// ### Exact effect of that request
//
// For the example above, assuming a container named `api` is running on both
// configured hosts and currently uses `ghcr.io/acme/api:1.2.2`:
//
// 1. Hosts **without** a running `api` container are skipped.
// 2. On each selected host, in order: pull `ghcr.io/acme/api:1.2.3`, snapshot
//    the running `api` container's create config, rename it aside as
//    `api_rollback_YYYYMMDDHHMMSS`, create and start a new `api` container
//    with the **same** env/labels/mounts/ports/networks/restart policy but
//    the new image, then remove the backup on success.
// 3. Fail-fast: if a later host fails, earlier hosts stay on `1.2.3` (HTTP
//    `207`), and the failed host is rolled back when possible.
// 4. A second overlapping deploy for the same container name returns HTTP
//    `409`.
//
// Successful responses are JSON listing per-host outcomes by `host_index`
// (index into `docker_hosts`). Docker host URLs and daemon error text are not
// returned in the body; see Caddy logs for details.
type Middleware struct {
	// Secret is the shared webhook credential. It must match the `{secret}`
	// path segment on every deploy request. Required.
	Secret string `json:"secret,omitempty"`

	// DockerHosts is the list of Docker Engine API endpoints to consider
	// (for example `unix:///var/run/docker.sock` or `tcp://host:2375`).
	// Only hosts where the named container is already running are updated.
	// Defaults to the local Docker socket when omitted.
	DockerHosts []string `json:"docker_hosts,omitempty"`

	logger   *zap.Logger
	clients  []*docker.Client
	deployer *deploy.Service
}

// Interface guards
var (
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddy.CleanerUpper          = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
)

// CaddyModule returns the Caddy module information.
func (Middleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.rolling_deployment",
		New: func() caddy.Module { return new(Middleware) },
	}
}

func init() {
	caddy.RegisterModule(Middleware{})
	httpcaddyfile.RegisterHandlerDirective("rolling_deployment", parseCaddyfile)
	httpcaddyfile.RegisterDirectiveOrder("rolling_deployment", httpcaddyfile.Before, "respond")
}

// ServeHTTP implements caddyhttp.MiddlewareHandler.
//
// Only /webhooks/rolling-deployment/{secret}/{service_container}/{service_image}
// is handled here (terminal). All other requests are passed to the next handler.
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if !strings.HasPrefix(r.URL.Path, webhookPrefix) {
		return next.ServeHTTP(w, r)
	}

	secret, container, image, ok := parseDeployPath(r.URL.Path)
	if !ok {
		return caddyhttp.Error(http.StatusBadRequest, errors.New("invalid path; expected /webhooks/rolling-deployment/{secret}/{service_container}/{service_image}"))
	}

	if subtle.ConstantTimeCompare([]byte(secret), []byte(m.Secret)) != 1 {
		return caddyhttp.Error(http.StatusUnauthorized, errors.New("invalid secret"))
	}

	if err := validateImageName(image); err != nil {
		return caddyhttp.Error(http.StatusBadRequest, err)
	}

	m.logger.Info("received deployment request",
		zap.String("service_container", container),
		zap.String("service_image", image),
		zap.Strings("docker_hosts", m.DockerHosts),
	)

	// Run the rolling deployment synchronously so the caller gets a real
	// status. Pulls can be slow; the request context bounds the work.
	res, err := m.deployer.Deploy(r.Context(), container, image)
	if res.Partial() {
		// Some hosts updated, then the roll stopped — leave them updated and
		// report multi-status so the caller knows the fleet is mixed.
		return writeDeployResult(w, http.StatusMultiStatus, res)
	}
	if err != nil {
		m.logger.Error("rolling deployment failed",
			zap.String("service_container", container),
			zap.String("service_image", image),
			zap.Error(err),
		)
		// Public label only — detailed Docker/host errors stay in server logs.
		return caddyhttp.Error(deploy.HTTPStatus(err), errors.New(deploy.PublicError(err)))
	}
	return writeDeployResult(w, http.StatusOK, res)
}

type deployHostResponse struct {
	HostIndex int    `json:"host_index"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

type deployResponse struct {
	Partial bool                 `json:"partial"`
	Hosts   []deployHostResponse `json:"hosts"`
}

func writeDeployResult(w http.ResponseWriter, status int, res deploy.Result) error {
	body := deployResponse{
		Partial: res.Partial(),
		Hosts:   make([]deployHostResponse, 0, len(res.Hosts)),
	}
	for _, h := range res.Hosts {
		item := deployHostResponse{
			HostIndex: h.Index,
			OK:        h.Err == nil,
		}
		if h.Err != nil {
			// Never return raw Docker host URLs or daemon error text to CI.
			item.Error = deploy.PublicError(h.Err)
		}
		body.Hosts = append(body.Hosts, item)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

// parseDeployPath extracts secret, container, and image from
// /webhooks/rolling-deployment/{secret}/{service_container}/{service_image}.
// service_image may contain slashes (e.g. ghcr.io/org/app:tag).
func parseDeployPath(path string) (secret, container, image string, ok bool) {
	if !strings.HasPrefix(path, webhookPrefix) {
		return "", "", "", false
	}

	rest := strings.TrimPrefix(path, webhookPrefix)
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}

	return parts[0], parts[1], parts[2], true
}

func validateImageName(image string) error {
	if image == "" {
		return errors.New("service_image is required")
	}
	if strings.Contains(image, "..") {
		return errors.New("service_image must not contain '..'")
	}
	if strings.ContainsAny(image, " \t\n\r") {
		return errors.New("service_image must not contain whitespace")
	}
	return nil
}

// Provision implements caddy.Provisioner.
func (m *Middleware) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger()

	if m.Secret == "" {
		return fmt.Errorf("secret is required")
	}

	if m.DockerHosts == nil {
		// default docker host (linux)
		m.DockerHosts = []string{
			"unix:///var/run/docker.sock",
		}
	}

	m.clients = make([]*docker.Client, 0, len(m.DockerHosts))
	for _, host := range m.DockerHosts {
		c, err := docker.New(host)
		if err != nil {
			return fmt.Errorf("create docker client for %q: %w", host, err)
		}
		m.clients = append(m.clients, c)
	}

	deployClients := make([]deploy.Client, len(m.clients))
	for i, c := range m.clients {
		deployClients[i] = c
	}
	m.deployer = deploy.NewService(m.logger, deployClients...)

	return nil
}

// Cleanup implements caddy.CleanerUpper, closing all Docker clients.
func (m *Middleware) Cleanup() error {
	var errs []error
	for _, c := range m.clients {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	m.clients = nil
	m.deployer = nil
	return errors.Join(errs...)
}

// Validate implements caddy.Validator.
func (m *Middleware) Validate() error {
	if m.Secret == "" {
		return errors.New("secret is required")
	}
	if len(m.DockerHosts) == 0 {
		return errors.New("docker_hosts is required")
	}
	return nil
}
