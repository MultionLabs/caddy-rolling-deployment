package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/moby/moby/client"
)

// DefaultHost returns the Docker host URL for the current environment.
//
// Resolution order:
//  1. DOCKER_HOST, if set
//  2. the first existing Docker socket among well-known local paths
//     (Docker Desktop on macOS/Linux home dirs, then /var/run/docker.sock)
//  3. the OS default from the Moby client (unix:///var/run/docker.sock or
//     the Windows named pipe)
func DefaultHost() string {
	if host := strings.TrimSpace(os.Getenv(client.EnvOverrideHost)); host != "" {
		return host
	}

	for _, sock := range localSocketPaths() {
		if fileExists(sock) {
			return "unix://" + sock
		}
	}

	return client.DefaultDockerHost
}

func localSocketPaths() []string {
	if runtime.GOOS == "windows" {
		return nil
	}

	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".docker", "run", "docker.sock"),
			filepath.Join(home, ".docker", "desktop", "docker.sock"),
		)
	}
	paths = append(paths, "/var/run/docker.sock")
	return paths
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
