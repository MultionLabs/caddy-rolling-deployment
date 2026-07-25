package rolling_deployment

import (
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestRollingDeployment_UnmarshalValidCaddyfile(t *testing.T) {
	config := `rolling_deployment {
		secret "test-secret"
		docker_hosts unix:///var/run/docker.sock unix:///var/run/docker.sock2
	}`

	d := caddyfile.NewTestDispenser(config)
	var m Middleware
	err := m.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("Failed to unmarshal caddyfile: %v", err)
	}

	if m.Secret != "test-secret" {
		t.Fatalf("Expected secret to be 'test-secret', got '%s'", m.Secret)
	}

	if len(m.DockerHosts) != 2 {
		t.Fatalf("Expected 2 docker hosts, got %d", len(m.DockerHosts))
	}
	if m.DockerHosts[0] != "unix:///var/run/docker.sock" {
		t.Fatalf("Expected docker host to be 'unix:///var/run/docker.sock', got '%s'", m.DockerHosts[0])
	}
	if m.DockerHosts[1] != "unix:///var/run/docker.sock2" {
		t.Fatalf("Expected docker host to be 'unix:///var/run/docker.sock2', got '%s'", m.DockerHosts[1])
	}
}
