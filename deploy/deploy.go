// Package deploy performs rolling container deployments across one or more
// Docker hosts, using docker.Client instances for the underlying operations.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/multionlabs/caddy-rolling-deployment/docker"
	"go.uber.org/zap"
)

// rollbackTimeout bounds best-effort rollback/cleanup work, which runs on a
// detached context so it still completes if the request context is cancelled.
const rollbackTimeout = 30 * time.Second

// Client is the subset of *docker.Client the deployer needs. It is satisfied
// by *docker.Client and can be faked in tests.
type Client interface {
	Host() string
	ListRunningNames(ctx context.Context) ([]string, error)
	Pull(ctx context.Context, image string) error
	Inspect(ctx context.Context, container string) (*docker.ContainerSpec, error)
	Rename(ctx context.Context, oldName, newName string) error
	Stop(ctx context.Context, container string) error
	Run(ctx context.Context, spec *docker.ContainerSpec) (string, error)
	Start(ctx context.Context, container string) error
	Remove(ctx context.Context, container string) error
}

var _ Client = (*docker.Client)(nil)

// ErrNotRunning means the named container was not found running on any
// configured Docker host.
var ErrNotRunning = errors.New("container is not running on any configured host")

// ErrIncompatible means the requested image could not be applied to the
// existing container (bad/missing image, or create/start failed with the
// preserved spec). The previous container is restored when possible.
var ErrIncompatible = errors.New("image is incompatible with the running container")

// ErrUnavailable means a Docker host/operation failed for infrastructural
// reasons (list/inspect/rename/stop, connectivity, etc.).
var ErrUnavailable = errors.New("docker host unavailable")

// ErrInProgress means a deployment for the same container is already running.
// Overlapping deploys are rejected rather than serialized to avoid interleaving
// rename/stop/run steps on the same container.
var ErrInProgress = errors.New("a deployment for this container is already in progress")

// HTTPStatus maps a Deploy error to an HTTP status for the webhook.
// Prefer Result.HTTPStatus when a Result is available (handles partial 207).
func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return 200
	case errors.Is(err, ErrNotRunning):
		return 400
	case errors.Is(err, ErrInProgress):
		return 409
	case errors.Is(err, ErrIncompatible):
		return 422
	case errors.Is(err, ErrUnavailable):
		return 502
	default:
		return 502
	}
}

// Service performs rolling deployments across a fixed set of Docker hosts.
type Service struct {
	logger  *zap.Logger
	clients []Client

	mu       sync.Mutex
	inflight map[string]struct{}
}

// NewService returns a Service that can deploy to the given clients.
func NewService(logger *zap.Logger, clients ...Client) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		logger:   logger,
		clients:  clients,
		inflight: make(map[string]struct{}),
	}
}

// tryAcquire marks container as being deployed. It returns false if a
// deployment for that container is already in flight.
func (s *Service) tryAcquire(container string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inflight[container]; busy {
		return false
	}
	s.inflight[container] = struct{}{}
	return true
}

// release clears the in-flight marker for container.
func (s *Service) release(container string) {
	s.mu.Lock()
	delete(s.inflight, container)
	s.mu.Unlock()
}

// HostResult is the outcome of a deployment on a single host.
type HostResult struct {
	// Index is the position of this host in the service's configured client
	// list (0-based). Prefer this over Host when returning data to callers.
	Index int
	Host  string
	Err   error
}

// Result aggregates per-host outcomes for one Deploy call.
type Result struct {
	Container string
	Image     string
	Hosts     []HostResult
}

// Err joins the errors from any failed hosts, or returns nil if all succeeded.
func (r Result) Err() error {
	var errs []error
	for _, h := range r.Hosts {
		if h.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h.Host, h.Err))
		}
	}
	return errors.Join(errs...)
}

// Partial reports whether at least one selected host was updated and at least
// one selected host failed. In that case the fleet is intentionally left in a
// mixed state (fail-fast rolling), and callers should surface HTTP 207.
func (r Result) Partial() bool {
	var ok, failed int
	for _, h := range r.Hosts {
		if h.Err != nil {
			failed++
		} else {
			ok++
		}
	}
	return ok > 0 && failed > 0
}

// HTTPStatus picks the webhook status for this result.
// Partial updates → 207; otherwise delegates to HTTPStatus(err) / 200.
func (r Result) HTTPStatus() int {
	if r.Partial() {
		return 207
	}
	if err := r.Err(); err != nil {
		return HTTPStatus(err)
	}
	return 200
}

// Deploy replaces container with a new instance running image, but only on
// hosts where that container is already running. Hosts without a matching
// running container are skipped. Selected hosts are updated one at a time; the
// first failure stops the roll so a bad image does not take down the rest of
// the matched fleet. Hosts already updated are left on the new image
// (partial success → Result.Partial() / HTTP 207).
func (s *Service) Deploy(ctx context.Context, container, image string) (Result, error) {
	res := Result{Container: container, Image: image}

	switch {
	case container == "":
		return res, errors.New("container is required")
	case image == "":
		return res, errors.New("image is required")
	case len(s.clients) == 0:
		return res, errors.New("no docker hosts configured")
	}

	// Reject overlapping deploys for the same container so their rename/stop/run
	// steps cannot interleave.
	if !s.tryAcquire(container) {
		s.logger.Warn("rejecting concurrent deployment",
			zap.String("container", container),
			zap.String("image", image),
		)
		return res, fmt.Errorf("%w: %q", ErrInProgress, container)
	}
	defer s.release(container)

	targets, err := s.hostsRunning(ctx, container)
	if err != nil {
		return res, err
	}
	if len(targets) == 0 {
		return res, fmt.Errorf("%w: %q", ErrNotRunning, container)
	}

	hostNames := make([]string, len(targets))
	for i, t := range targets {
		hostNames[i] = t.client.Host()
	}
	s.logger.Info("selected hosts for rolling deployment",
		zap.String("container", container),
		zap.String("image", image),
		zap.Int("hosts", len(targets)),
		zap.Strings("docker_hosts", hostNames),
	)

	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			res.Hosts = append(res.Hosts, HostResult{Index: t.index, Host: t.client.Host(), Err: err})
			return res, res.Err()
		}

		err := s.deployOnHost(ctx, t.client, container, image)
		res.Hosts = append(res.Hosts, HostResult{Index: t.index, Host: t.client.Host(), Err: err})
		if err != nil {
			s.logger.Error("rolling deployment failed on host",
				zap.String("host", t.client.Host()),
				zap.Int("host_index", t.index),
				zap.String("container", container),
				zap.String("image", image),
				zap.Bool("partial", res.Partial()),
				zap.Error(err),
			)
			return res, res.Err()
		}

		s.logger.Info("rolling deployment succeeded on host",
			zap.String("host", t.client.Host()),
			zap.Int("host_index", t.index),
			zap.String("container", container),
			zap.String("image", image),
		)
	}

	return res, nil
}

// PublicError returns a short, non-sensitive error label suitable for HTTP
// responses and CI logs. Detailed causes stay in server logs only.
func PublicError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotRunning):
		return ErrNotRunning.Error()
	case errors.Is(err, ErrInProgress):
		return ErrInProgress.Error()
	case errors.Is(err, ErrIncompatible):
		return ErrIncompatible.Error()
	case errors.Is(err, ErrUnavailable):
		return ErrUnavailable.Error()
	case errors.Is(err, context.Canceled):
		return "deployment canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deployment timed out"
	default:
		return "deployment failed"
	}
}

// target is a configured client selected for a deploy, with its config index.
type target struct {
	client Client
	index  int
}

// hostsRunning returns the configured clients on which container is currently
// running (matched by exact container name). A failure to list containers on
// any host aborts selection so we never silently skip a host we could not
// inspect.
func (s *Service) hostsRunning(ctx context.Context, container string) ([]target, error) {
	var selected []target
	for i, c := range s.clients {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		names, err := c.ListRunningNames(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: list running containers on %s: %v", ErrUnavailable, c.Host(), err)
		}

		if slices.Contains(names, container) {
			selected = append(selected, target{client: c, index: i})
			continue
		}

		s.logger.Info("skipping host; container not running",
			zap.String("host", c.Host()),
			zap.Int("host_index", i),
			zap.String("container", container),
		)
	}
	return selected, nil
}

// deployOnHost performs the replace-with-rollback sequence on a single host:
//
//	inspect -> pull -> rename old aside -> stop old -> run new -> remove old
//
// If a step fails after the old container has been moved aside, the previous
// container is restored as the live one.
func (s *Service) deployOnHost(ctx context.Context, c Client, container, image string) error {
	log := s.logger.With(
		zap.String("host", c.Host()),
		zap.String("container", container),
		zap.String("image", image),
	)

	log.Info("starting host deployment")

	// Snapshot how the current container was created so the replacement keeps
	// the same volumes, networks, env, ports, restart policy, etc.
	spec, err := c.Inspect(ctx, container)
	if err != nil {
		return fmt.Errorf("%w: inspect current container: %v", ErrUnavailable, err)
	}
	log.Info("inspected current container spec", specFields("spec", spec)...)

	log.Info("pulling image")
	if err := c.Pull(ctx, image); err != nil {
		return fmt.Errorf("%w: pull image: %v", ErrIncompatible, err)
	}
	log.Info("pulled image")

	// Move the old container aside to free its name and published ports.
	backup := backupName(container)
	log.Info("renaming current container aside",
		zap.String("from", container),
		zap.String("backup", backup),
	)
	if err := c.Rename(ctx, container, backup); err != nil {
		return fmt.Errorf("%w: rename current container aside: %v", ErrUnavailable, err)
	}

	log.Info("stopping backup container", zap.String("backup", backup))
	if err := c.Stop(ctx, backup); err != nil {
		log.Error("stop failed; rolling back", zap.String("backup", backup), zap.Error(err))
		s.rollback(c, backup, container)
		return fmt.Errorf("%w: stop current container: %v", ErrUnavailable, err)
	}

	// Create and start the replacement under the original name and new image.
	newSpec := spec.WithImage(image).WithName(container)
	log.Info("creating replacement container", specFields("new_spec", newSpec)...)
	newID, err := c.Run(ctx, newSpec)
	if err != nil {
		log.Error("run failed; rolling back",
			zap.String("backup", backup),
			zap.Error(err),
		)
		s.rollback(c, backup, container)
		return fmt.Errorf("%w: run new container: %v", ErrIncompatible, err)
	}
	log.Info("replacement container running",
		zap.String("new_id", newID),
		zap.String("backup", backup),
	)

	// New container is live; drop the old one. Failure here is non-fatal.
	log.Info("removing backup container", zap.String("backup", backup))
	if err := c.Remove(ctx, backup); err != nil {
		log.Warn("failed to remove old container after successful deploy",
			zap.String("backup", backup),
			zap.Error(err),
		)
	} else {
		log.Info("removed backup container", zap.String("backup", backup))
	}

	return nil
}

// rollback restores the previous container (currently renamed to backup) as the
// live container under original. It is best-effort: it runs on a detached,
// time-bounded context and only logs failures.
func (s *Service) rollback(c Client, backup, original string) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()

	s.logger.Warn("rolling back to previous container",
		zap.String("host", c.Host()),
		zap.String("backup", backup),
		zap.String("original", original),
	)

	if err := c.Rename(ctx, backup, original); err != nil {
		s.logger.Error("rollback: failed to rename previous container back",
			zap.String("host", c.Host()),
			zap.String("backup", backup),
			zap.String("original", original),
			zap.Error(err),
		)
		return
	}
	s.logger.Info("rollback: renamed backup back to original name",
		zap.String("host", c.Host()),
		zap.String("container", original),
	)

	if err := c.Start(ctx, original); err != nil {
		s.logger.Error("rollback: failed to restart previous container",
			zap.String("host", c.Host()),
			zap.String("container", original),
			zap.Error(err),
		)
		return
	}
	s.logger.Info("rollback: previous container restarted",
		zap.String("host", c.Host()),
		zap.String("container", original),
	)
}

// backupName returns a temporary name for the outgoing container.
// The timestamp is UTC YYYYMMDDHHMMSS so leftovers are easy to spot in docker ps.
func backupName(container string) string {
	return fmt.Sprintf("%s_rollback_%s", container, time.Now().UTC().Format("20060102150405"))
}
