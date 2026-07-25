package rolling_deployment

import "testing"

func TestParseDeployPath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		secret    string
		container string
		image     string
		ok        bool
	}{
		{
			name:      "simple",
			path:      "/webhooks/rolling-deployment/s3cret/my-app/alpine:latest",
			secret:    "s3cret",
			container: "my-app",
			image:     "alpine:latest",
			ok:        true,
		},
		{
			name:      "image with registry path",
			path:      "/webhooks/rolling-deployment/s3cret/my-app/ghcr.io/org/app:1.2.3",
			secret:    "s3cret",
			container: "my-app",
			image:     "ghcr.io/org/app:1.2.3",
			ok:        true,
		},
		{
			name: "unrelated path",
			path: "/healthz",
			ok:   false,
		},
		{
			name: "missing image",
			path: "/webhooks/rolling-deployment/s3cret/my-app",
			ok:   false,
		},
		{
			name: "missing container and image",
			path: "/webhooks/rolling-deployment/s3cret",
			ok:   false,
		},
		{
			name: "empty after prefix",
			path: "/webhooks/rolling-deployment/",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, container, image, ok := parseDeployPath(tt.path)
			if ok != tt.ok {
				t.Fatalf("ok=%v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if secret != tt.secret || container != tt.container || image != tt.image {
				t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)",
					secret, container, image, tt.secret, tt.container, tt.image)
			}
		})
	}
}

func TestValidateImageName(t *testing.T) {
	if err := validateImageName("alpine:latest"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateImageName("ghcr.io/org/app:1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateImageName(""); err == nil {
		t.Fatal("expected error for empty image")
	}
	if err := validateImageName("evil/../hack"); err == nil {
		t.Fatal("expected error for '..'")
	}
	if err := validateImageName("bad image"); err == nil {
		t.Fatal("expected error for whitespace")
	}
}
