package docker

import (
	"net/netip"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

func TestSpecFromInspect(t *testing.T) {
	inspect := container.InspectResponse{
		Name: "/svc",
		HostConfig: &container.HostConfig{
			Binds:       []string{"/data:/data:rw"},
			NetworkMode: "bridge",
			RestartPolicy: container.RestartPolicy{
				Name: "unless-stopped",
			},
		},
		Config: &container.Config{
			Image:  "app:old",
			Env:    []string{"FOO=bar"},
			Labels: map[string]string{"com.example": "1"},
			Volumes: map[string]struct{}{
				"/data": {},
			},
		},
		NetworkSettings: &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"frontend": {
					IPAddress:  netip.MustParseAddr("172.18.0.10"),
					MacAddress: mustHWAddr(t, "aa:bb:cc:dd:ee:ff"),
					Aliases:    []string{"svc"},
					NetworkID:  "net-operational-id",
					EndpointID: "ep-operational-id",
					IPAMConfig: &network.EndpointIPAMConfig{
						IPv4Address: netip.MustParseAddr("172.18.0.10"),
					},
				},
			},
		},
	}

	spec, err := specFromInspect(inspect)
	if err != nil {
		t.Fatalf("specFromInspect: %v", err)
	}

	if spec.Name != "" {
		t.Fatalf("Name = %q, want empty (caller must WithName after freeing it)", spec.Name)
	}
	if spec.Config.Image != "app:old" {
		t.Fatalf("Image = %q, want app:old", spec.Config.Image)
	}
	if spec.Config.Labels["com.example"] != "1" {
		t.Fatalf("Labels not preserved: %#v", spec.Config.Labels)
	}
	if len(spec.HostConfig.Binds) != 1 || spec.HostConfig.Binds[0] != "/data:/data:rw" {
		t.Fatalf("Binds not preserved: %#v", spec.HostConfig.Binds)
	}

	ep := spec.NetworkingConfig.EndpointsConfig["frontend"]
	if ep == nil {
		t.Fatal("expected frontend endpoint")
	}
	if ep.NetworkID != "" || ep.EndpointID != "" {
		t.Fatalf("operational IDs should be dropped, got NetworkID=%q EndpointID=%q", ep.NetworkID, ep.EndpointID)
	}
	if ep.IPAMConfig != nil {
		t.Fatalf("IPAM/IPs should not be copied (would conflict), got %#v", ep.IPAMConfig)
	}
	if !ep.IPAddress.IsValid() && len(ep.MacAddress) == 0 {
		// good: no assigned address/MAC carried over
	} else {
		t.Fatalf("assigned IP/MAC should not be copied, got IP=%v MAC=%v", ep.IPAddress, ep.MacAddress)
	}
	if len(ep.Aliases) != 1 || ep.Aliases[0] != "svc" {
		t.Fatalf("Aliases not preserved: %#v", ep.Aliases)
	}

	replaced := spec.WithImage("app:new").WithName("svc")
	if replaced.Config.Image != "app:new" {
		t.Fatalf("WithImage Image = %q, want app:new", replaced.Config.Image)
	}
	if spec.Config.Image != "app:old" {
		t.Fatalf("WithImage mutated original Image to %q", spec.Config.Image)
	}
	if replaced.Name != "svc" {
		t.Fatalf("WithName Name = %q, want svc", replaced.Name)
	}
	if spec.Name != "" {
		t.Fatalf("WithName mutated original Name to %q", spec.Name)
	}
}

func mustHWAddr(t *testing.T, s string) network.HardwareAddr {
	t.Helper()
	var hw network.HardwareAddr
	if err := hw.UnmarshalText([]byte(s)); err != nil {
		t.Fatalf("parse MAC %q: %v", s, err)
	}
	return hw
}
