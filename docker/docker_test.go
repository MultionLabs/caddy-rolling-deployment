package docker

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func Test_BasicDockerOperations(t *testing.T) {
	ctx := context.Background()

	host := DefaultHost()
	c, err := New(host)
	if err != nil {
		t.Skipf("docker not available (%s): %v", host, err)
	}
	t.Logf("docker host: %s", c.Host())
	defer func() { _ = c.Close() }()

	const image = "alpine:latest"
	if err = c.Pull(ctx, image); err != nil {
		t.Skipf("docker not available: %v", err)
	} else {
		t.Logf("pulled image: %v", image)
	}

	const name = "rolling-deployment-list-test"
	id, err := c.Run(ctx, &ContainerSpec{
		Name: name,
		Config: &container.Config{
			Image: image,
			Cmd:   []string{"sleep", "60"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	} else {
		t.Logf("ran container: %v", id)
	}
	defer func() {
		_ = c.Stop(ctx, name)
		_ = c.Remove(ctx, name)
	}()

	names, err := c.ListRunningNames(ctx)
	if err != nil {
		t.Fatalf("ListRunningNames: %v", err)
	}
	found := false
	for _, n := range names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListRunningNames missing %q, got %v", name, names)
	}
	t.Logf("running containers: %v", names)

	spec, err := c.Inspect(ctx, name)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	} else {
		t.Logf("inspected container: %v", spec)
	}

	if err := c.Stop(ctx, name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Remove(ctx, name); err != nil {
		t.Fatalf("Remove: %v", err)
	} else {
		t.Logf("removed container: %v", name)
	}
}
