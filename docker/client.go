// Package docker provides Docker Engine operations used by rolling deployments.
//
// Each Client talks to a single Docker host (unix socket or TCP URL).
package docker

import (
	"fmt"

	"github.com/moby/moby/client"
)

// Client wraps a Docker Engine API client bound to one host.
type Client struct {
	host string
	cli  *client.Client
}

// New creates a Client for the given Docker host.
//
// host is a Docker host URL such as:
//   - unix:///var/run/docker.sock
//   - tcp://192.168.1.10:2375
//   - ssh://user@host
func New(host string) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("docker host is required")
	}

	cli, err := client.New(client.WithHost(host))
	if err != nil {
		return nil, fmt.Errorf("create docker client for %q: %w", host, err)
	}

	return &Client{host: host, cli: cli}, nil
}

// Host returns the Docker host URL this client is bound to.
func (c *Client) Host() string {
	return c.host
}

// Close releases resources held by the underlying Docker client.
func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}
