package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
)

// Pull pulls imageName from the registry configured for this Docker host.
func (c *Client) Pull(ctx context.Context, imageName string) error {
	if imageName == "" {
		return fmt.Errorf("image name is required")
	}

	resp, err := c.cli.ImagePull(ctx, imageName, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %q on %s: %w", imageName, c.host, err)
	}
	defer func() { _ = resp.Close() }()

	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("pull %q on %s: %w", imageName, c.host, err)
	}
	return nil
}

// Rename renames a container from oldContainerName to newContainerName.
func (c *Client) Rename(ctx context.Context, oldContainerName, newContainerName string) error {
	if oldContainerName == "" || newContainerName == "" {
		return fmt.Errorf("old and new container names are required")
	}
	if _, err := c.cli.ContainerRename(ctx, oldContainerName, client.ContainerRenameOptions{
		NewName: newContainerName,
	}); err != nil {
		return fmt.Errorf("rename %q -> %q on %s: %w", oldContainerName, newContainerName, c.host, err)
	}
	return nil
}

// Stop stops the named container.
func (c *Client) Stop(ctx context.Context, containerName string) error {
	if containerName == "" {
		return fmt.Errorf("container name is required")
	}
	if _, err := c.cli.ContainerStop(ctx, containerName, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("stop %q on %s: %w", containerName, c.host, err)
	}
	return nil
}

// Inspect returns the ContainerSpec that uniquely identifies how the named
// container was created (labels, volumes/mounts, networks, env, ports, etc.).
func (c *Client) Inspect(ctx context.Context, containerName string) (*ContainerSpec, error) {
	if containerName == "" {
		return nil, fmt.Errorf("container name is required")
	}

	result, err := c.cli.ContainerInspect(ctx, containerName, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect %q on %s: %w", containerName, c.host, err)
	}

	spec, err := specFromInspect(result.Container)
	if err != nil {
		return nil, fmt.Errorf("inspect %q on %s: %w", containerName, c.host, err)
	}
	return spec, nil
}

// Run creates and starts a container from spec (typically produced by Inspect,
// then adjusted via WithImage / WithName).
//
// Spec from Inspect has no name and no endpoint IPs/MACs, so it will not
// collide with the inspected container on those. Free the desired name (and
// published ports) first — e.g. Rename + Stop — then WithName before Run.
// It returns the new container ID.
func (c *Client) Run(ctx context.Context, spec *ContainerSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("container spec is required")
	}
	if spec.Config == nil {
		return "", fmt.Errorf("container spec config is required")
	}
	if spec.Config.Image == "" {
		return "", fmt.Errorf("container spec image is required")
	}

	create, err := c.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           spec.Config,
		HostConfig:       spec.HostConfig,
		NetworkingConfig: spec.NetworkingConfig,
		Name:             spec.Name,
	})
	if err != nil {
		return "", fmt.Errorf("create %q from image %q on %s: %w", spec.Name, spec.Config.Image, c.host, err)
	}

	if _, err := c.cli.ContainerStart(ctx, create.ID, client.ContainerStartOptions{}); err != nil {
		// Best-effort cleanup so a failed start does not leave an orphan.
		_, _ = c.cli.ContainerRemove(ctx, create.ID, client.ContainerRemoveOptions{Force: true})
		return "", fmt.Errorf("start %q (%s) on %s: %w", spec.Name, create.ID, c.host, err)
	}

	return create.ID, nil
}

// Remove removes the named container.
func (c *Client) Remove(ctx context.Context, containerName string) error {
	if containerName == "" {
		return fmt.Errorf("container name is required")
	}
	if _, err := c.cli.ContainerRemove(ctx, containerName, client.ContainerRemoveOptions{}); err != nil {
		return fmt.Errorf("remove %q on %s: %w", containerName, c.host, err)
	}
	return nil
}

// Start starts an existing stopped container.
func (c *Client) Start(ctx context.Context, containerName string) error {
	if containerName == "" {
		return fmt.Errorf("container name is required")
	}
	if _, err := c.cli.ContainerStart(ctx, containerName, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start %q on %s: %w", containerName, c.host, err)
	}
	return nil
}

// ListRunningNames returns the names of containers currently running on this
// host, without Docker's leading slash.
func (c *Client) ListRunningNames(ctx context.Context) ([]string, error) {
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list running containers on %s: %w", c.host, err)
	}

	var names []string
	for _, summary := range result.Items {
		for _, name := range summary.Names {
			name = strings.TrimPrefix(name, "/")
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}
