package docker

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// ContainerSpec captures recreate parameters from docker inspect: labels, env,
// mounts/volumes, network membership, ports, restart policy, and related
// host/config settings.
//
// Inspect deliberately omits instance identity that would conflict with the
// still-existing source container:
//   - Name is left empty (set explicitly via WithName after Rename frees it)
//   - Endpoint IPs / MAC addresses are not copied (Docker allocates new ones)
//
// Callers are expected to free conflicting host resources (name, published
// ports) before Run — typically Rename + Stop the old container first.
type ContainerSpec struct {
	// Name is the container name without a leading slash.
	// Empty after Inspect; set with WithName before Run when a name is desired.
	Name string

	Config           *container.Config
	HostConfig       *container.HostConfig
	NetworkingConfig *network.NetworkingConfig
}

// WithImage returns a shallow copy of the spec with Config.Image set to image.
// The original spec is not modified.
func (s *ContainerSpec) WithImage(image string) *ContainerSpec {
	if s == nil {
		return nil
	}
	out := *s
	if s.Config != nil {
		cfg := *s.Config
		cfg.Image = image
		out.Config = &cfg
	} else {
		out.Config = &container.Config{Image: image}
	}
	return &out
}

// WithName returns a shallow copy of the spec with Name set.
// Use only after the previous holder of that name has been renamed or removed.
func (s *ContainerSpec) WithName(name string) *ContainerSpec {
	if s == nil {
		return nil
	}
	out := *s
	out.Name = strings.TrimPrefix(name, "/")
	return &out
}

func specFromInspect(inspect container.InspectResponse) (*ContainerSpec, error) {
	if inspect.Config == nil {
		return nil, fmt.Errorf("inspect response missing container config")
	}
	if inspect.HostConfig == nil {
		return nil, fmt.Errorf("inspect response missing host config")
	}

	cfg := *inspect.Config
	hostCfg := *inspect.HostConfig

	return &ContainerSpec{
		// Name intentionally unset: reusing the inspected name would conflict
		// with the still-existing container. Caller sets it via WithName.
		Config:           &cfg,
		HostConfig:       &hostCfg,
		NetworkingConfig: networkingConfigFromInspect(inspect),
	}, nil
}

func networkingConfigFromInspect(inspect container.InspectResponse) *network.NetworkingConfig {
	if inspect.NetworkSettings == nil || len(inspect.NetworkSettings.Networks) == 0 {
		return nil
	}

	endpoints := make(map[string]*network.EndpointSettings, len(inspect.NetworkSettings.Networks))
	for name, ep := range inspect.NetworkSettings.Networks {
		if ep == nil {
			continue
		}
		endpoints[name] = endpointForCreate(ep)
	}
	if len(endpoints) == 0 {
		return nil
	}
	return &network.NetworkingConfig{EndpointsConfig: endpoints}
}

// endpointForCreate keeps network-membership config (aliases, links, driver
// opts) and drops instance identity: operational IDs, assigned IPs, and MAC.
// Reusing those while the source container is still attached would conflict.
func endpointForCreate(ep *network.EndpointSettings) *network.EndpointSettings {
	return &network.EndpointSettings{
		Links:      slices.Clone(ep.Links),
		Aliases:    slices.Clone(ep.Aliases),
		DriverOpts: maps.Clone(ep.DriverOpts),
		GwPriority: ep.GwPriority,
	}
}
