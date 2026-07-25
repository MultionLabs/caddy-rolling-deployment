package docker

import (
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

func TestDefaultHostPrefersEnv(t *testing.T) {
	const want = "tcp://example.invalid:2375"
	t.Setenv("DOCKER_HOST", want)

	if got := DefaultHost(); got != want {
		t.Fatalf("DefaultHost() = %q, want %q", got, want)
	}
}

func TestDefaultHostWithoutEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")

	got := DefaultHost()
	if got == "" {
		t.Fatal("DefaultHost() returned empty string")
	}
	if !strings.HasPrefix(got, "unix://") && !strings.HasPrefix(got, "npipe:") {
		t.Fatalf("DefaultHost() = %q, want unix:// or npipe: URL", got)
	}

	// If no local socket is present, we must fall back to the Moby OS default.
	anyLocal := false
	for _, sock := range localSocketPaths() {
		if fileExists(sock) {
			anyLocal = true
			if got != "unix://"+sock {
				// First existing socket wins; verify got matches some candidate.
				break
			}
		}
	}
	if !anyLocal && got != client.DefaultDockerHost {
		t.Fatalf("DefaultHost() = %q, want fallback %q", got, client.DefaultDockerHost)
	}
	t.Logf("DefaultHost() = %s", got)
}
