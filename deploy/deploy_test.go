package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/multionlabs/caddy-rolling-deployment/docker"
)

// fakeClient records the operations invoked on it and can be told to fail at a
// specific step. It stands in for *docker.Client in tests.
type fakeClient struct {
	host string

	running     []string
	failList    bool
	failInspect bool
	failPull    bool
	failStop    bool
	failRun     bool
	failRemove  bool

	// listStarted (if non-nil) is closed the first time ListRunningNames runs,
	// and listGate (if non-nil) blocks ListRunningNames until it is closed.
	// Together they let a test hold a deployment in flight.
	listStarted chan struct{}
	listGate    chan struct{}

	calls []string
}

func (f *fakeClient) Host() string { return f.host }

func (f *fakeClient) ListRunningNames(_ context.Context) ([]string, error) {
	f.calls = append(f.calls, "list")
	if f.listStarted != nil {
		close(f.listStarted)
		f.listStarted = nil
	}
	if f.listGate != nil {
		<-f.listGate
	}
	if f.failList {
		return nil, errors.New("list failed")
	}
	out := make([]string, len(f.running))
	copy(out, f.running)
	return out, nil
}

func (f *fakeClient) Pull(_ context.Context, image string) error {
	f.calls = append(f.calls, "pull "+image)
	if f.failPull {
		return errors.New("pull failed")
	}
	return nil
}

func (f *fakeClient) Inspect(_ context.Context, name string) (*docker.ContainerSpec, error) {
	f.calls = append(f.calls, "inspect "+name)
	if f.failInspect {
		return nil, errors.New("inspect failed")
	}
	return &docker.ContainerSpec{Config: &container.Config{Image: "app:old"}}, nil
}

func (f *fakeClient) Rename(_ context.Context, oldName, newName string) error {
	f.calls = append(f.calls, "rename "+oldName+"->"+newName)
	return nil
}

func (f *fakeClient) Stop(_ context.Context, name string) error {
	f.calls = append(f.calls, "stop "+name)
	if f.failStop {
		return errors.New("stop failed")
	}
	return nil
}

func (f *fakeClient) Run(_ context.Context, spec *docker.ContainerSpec) (string, error) {
	f.calls = append(f.calls, "run "+spec.Name+" "+spec.Config.Image)
	if f.failRun {
		return "", errors.New("run failed")
	}
	return "new-id", nil
}

func (f *fakeClient) Start(_ context.Context, name string) error {
	f.calls = append(f.calls, "start "+name)
	return nil
}

func (f *fakeClient) Remove(_ context.Context, name string) error {
	f.calls = append(f.calls, "remove "+name)
	if f.failRemove {
		return errors.New("remove failed")
	}
	return nil
}

func opKinds(calls []string) []string {
	kinds := make([]string, len(calls))
	for i, c := range calls {
		kinds[i] = strings.SplitN(c, " ", 2)[0]
	}
	return kinds
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPublicErrorOmitsDetails(t *testing.T) {
	err := fmt.Errorf("%w: pull image: dial tcp 10.0.0.5:2375: connection refused", ErrUnavailable)
	got := PublicError(err)
	if got != ErrUnavailable.Error() {
		t.Fatalf("PublicError = %q, want %q", got, ErrUnavailable.Error())
	}
	if strings.Contains(got, "10.0.0.5") || strings.Contains(got, "2375") {
		t.Fatalf("PublicError leaked host details: %q", got)
	}
}

func TestDeployHappyPathSelectedHosts(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"app"}}
	h2 := &fakeClient{host: "unix:///h2", running: []string{"app", "other"}}
	svc := NewService(nil, h1, h2)

	res, err := svc.Deploy(context.Background(), "app", "app:new")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res.Hosts) != 2 || res.Err() != nil {
		t.Fatalf("expected both hosts to succeed, got %+v", res)
	}

	want := []string{"list", "inspect", "pull", "rename", "stop", "run", "remove"}
	for _, h := range []*fakeClient{h1, h2} {
		if got := opKinds(h.calls); !equalStrings(got, want) {
			t.Fatalf("host %s ops = %v, want %v", h.host, got, want)
		}
	}
}

func TestDeploySkipsHostsWithoutRunningContainer(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"other"}}
	h2 := &fakeClient{host: "unix:///h2", running: []string{"app"}}
	svc := NewService(nil, h1, h2)

	res, err := svc.Deploy(context.Background(), "app", "app:new")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if len(res.Hosts) != 1 || res.Hosts[0].Host != "unix:///h2" {
		t.Fatalf("expected only h2 to be deployed, got %+v", res.Hosts)
	}

	if got := opKinds(h1.calls); !equalStrings(got, []string{"list"}) {
		t.Fatalf("host1 ops = %v, want [list]", got)
	}
	want := []string{"list", "inspect", "pull", "rename", "stop", "run", "remove"}
	if got := opKinds(h2.calls); !equalStrings(got, want) {
		t.Fatalf("host2 ops = %v, want %v", got, want)
	}
}

func TestDeployErrorsWhenContainerNotRunningAnywhere(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"other"}}
	h2 := &fakeClient{host: "unix:///h2", running: nil}
	svc := NewService(nil, h1, h2)

	if _, err := svc.Deploy(context.Background(), "app", "app:new"); err == nil {
		t.Fatal("expected error when container is not running on any host")
	} else if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning, got %v", err)
	}
	if got := opKinds(h1.calls); !equalStrings(got, []string{"list"}) {
		t.Fatalf("host1 ops = %v, want [list]", got)
	}
	if got := opKinds(h2.calls); !equalStrings(got, []string{"list"}) {
		t.Fatalf("host2 ops = %v, want [list]", got)
	}
}

func TestDeployListFailureAborts(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", failList: true}
	h2 := &fakeClient{host: "unix:///h2", running: []string{"app"}}
	svc := NewService(nil, h1, h2)

	if _, err := svc.Deploy(context.Background(), "app", "app:new"); err == nil {
		t.Fatal("expected error when listing fails")
	} else if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if len(h2.calls) != 0 {
		t.Fatalf("second host should not be touched after list failure, got %v", h2.calls)
	}
}

func TestDeployPartialSuccessWhenLaterHostFails(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"app"}}
	h2 := &fakeClient{host: "unix:///h2", running: []string{"app"}, failRun: true}
	svc := NewService(nil, h1, h2)

	res, err := svc.Deploy(context.Background(), "app", "app:new")
	if err == nil {
		t.Fatal("expected error from failing host")
	}
	if !res.Partial() {
		t.Fatalf("expected partial result, got hosts=%+v", res.Hosts)
	}
	if got := res.HTTPStatus(); got != 207 {
		t.Fatalf("HTTPStatus = %d, want 207", got)
	}
	if len(res.Hosts) != 2 {
		t.Fatalf("expected 2 host results, got %d", len(res.Hosts))
	}
	if res.Hosts[0].Index != 0 || res.Hosts[1].Index != 1 {
		t.Fatalf("host indexes = [%d %d], want [0 1]", res.Hosts[0].Index, res.Hosts[1].Index)
	}
	if res.Hosts[0].Err != nil {
		t.Fatalf("first host should have succeeded, got %v", res.Hosts[0].Err)
	}
	if res.Hosts[1].Err == nil {
		t.Fatal("second host should have failed")
	}
	if !errors.Is(res.Hosts[1].Err, ErrIncompatible) {
		t.Fatalf("second host err = %v, want ErrIncompatible", res.Hosts[1].Err)
	}

	// First host completed the full replace sequence.
	want1 := []string{"list", "inspect", "pull", "rename", "stop", "run", "remove"}
	if got := opKinds(h1.calls); !equalStrings(got, want1) {
		t.Fatalf("host1 ops = %v, want %v", got, want1)
	}
	// Second host rolled back after run failure.
	want2 := []string{"list", "inspect", "pull", "rename", "stop", "run", "rename", "start"}
	if got := opKinds(h2.calls); !equalStrings(got, want2) {
		t.Fatalf("host2 ops = %v, want %v", got, want2)
	}
}

func TestDeployRunFailureRollsBackAndStops(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"app"}, failRun: true}
	h2 := &fakeClient{host: "unix:///h2", running: []string{"app"}}
	svc := NewService(nil, h1, h2)

	res, err := svc.Deploy(context.Background(), "app", "app:new")
	if err == nil {
		t.Fatal("expected error when run fails")
	}
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
	if HTTPStatus(err) != 422 {
		t.Fatalf("HTTPStatus = %d, want 422", HTTPStatus(err))
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("expected to stop after first selected host, got %d host results", len(res.Hosts))
	}

	want := []string{"list", "inspect", "pull", "rename", "stop", "run", "rename", "start"}
	if got := opKinds(h1.calls); !equalStrings(got, want) {
		t.Fatalf("host1 ops = %v, want %v", got, want)
	}
	// h2 was selected during discovery, but must not be deployed after h1 failed.
	if got := opKinds(h2.calls); !equalStrings(got, []string{"list"}) {
		t.Fatalf("host2 ops = %v, want [list]", got)
	}
}

func TestDeployPullFailureLeavesContainerUntouched(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"app"}, failPull: true}
	svc := NewService(nil, h1)

	if _, err := svc.Deploy(context.Background(), "app", "app:new"); err == nil {
		t.Fatal("expected error when pull fails")
	} else if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}

	want := []string{"list", "inspect", "pull"}
	if got := opKinds(h1.calls); !equalStrings(got, want) {
		t.Fatalf("host1 ops = %v, want %v", got, want)
	}
}

func TestDeployRemoveFailureStillSucceeds(t *testing.T) {
	h1 := &fakeClient{host: "unix:///h1", running: []string{"app"}, failRemove: true}
	svc := NewService(nil, h1)

	res, err := svc.Deploy(context.Background(), "app", "app:new")
	if err != nil {
		t.Fatalf("remove failure should be non-fatal, got %v", err)
	}
	if res.Err() != nil {
		t.Fatalf("expected success despite remove failure, got %v", res.Err())
	}
}

func TestDeployRejectsConcurrentSameContainer(t *testing.T) {
	started := make(chan struct{})
	gate := make(chan struct{})
	h := &fakeClient{
		host:        "unix:///h1",
		running:     []string{"app"},
		listStarted: started,
		listGate:    gate,
	}
	svc := NewService(nil, h)

	firstErr := make(chan error, 1)
	go func() {
		_, err := svc.Deploy(context.Background(), "app", "app:new")
		firstErr <- err
	}()

	// Wait until the first deploy has acquired the container and is in flight.
	<-started

	// A second deploy for the same container must be rejected immediately.
	if _, err := svc.Deploy(context.Background(), "app", "app:other"); !errors.Is(err, ErrInProgress) {
		close(gate)
		t.Fatalf("expected ErrInProgress, got %v", err)
	} else if HTTPStatus(err) != 409 {
		close(gate)
		t.Fatalf("HTTPStatus = %d, want 409", HTTPStatus(err))
	}

	// Let the first deploy finish, then verify the lock is released.
	close(gate)
	if err := <-firstErr; err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	// A subsequent deploy for the same container should now be allowed.
	h2 := &fakeClient{host: "unix:///h1", running: []string{"app"}}
	svc2 := NewService(nil, h2)
	if _, err := svc2.Deploy(context.Background(), "app", "app:new"); err != nil {
		t.Fatalf("deploy after release should succeed, got %v", err)
	}
}

func TestDeployDifferentContainersRunConcurrently(t *testing.T) {
	// Two different containers must not block each other.
	startedA := make(chan struct{})
	gateA := make(chan struct{})
	hA := &fakeClient{host: "unix:///h1", running: []string{"a"}, listStarted: startedA, listGate: gateA}
	svcA := NewService(nil, hA)

	doneA := make(chan error, 1)
	go func() {
		_, err := svcA.Deploy(context.Background(), "a", "a:new")
		doneA <- err
	}()
	<-startedA

	// Different container on a separate service proceeds without waiting on A.
	hB := &fakeClient{host: "unix:///h2", running: []string{"b"}}
	svcB := NewService(nil, hB)
	if _, err := svcB.Deploy(context.Background(), "b", "b:new"); err != nil {
		close(gateA)
		t.Fatalf("deploy of different container should not block, got %v", err)
	}

	close(gateA)
	if err := <-doneA; err != nil {
		t.Fatalf("deploy A: %v", err)
	}
}

func TestDeployValidatesInput(t *testing.T) {
	svc := NewService(nil, &fakeClient{host: "unix:///h1", running: []string{"app"}})

	if _, err := svc.Deploy(context.Background(), "", "app:new"); err == nil {
		t.Fatal("expected error for empty container")
	}
	if _, err := svc.Deploy(context.Background(), "app", ""); err == nil {
		t.Fatal("expected error for empty image")
	}

	empty := NewService(nil)
	if _, err := empty.Deploy(context.Background(), "app", "app:new"); err == nil {
		t.Fatal("expected error when no hosts configured")
	}
}
