package build

import (
	"testing"
)

func TestDefaultFlavorOptions(t *testing.T) {
	options := DefaultFlavorOptions()

	if len(options) == 0 {
		t.Fatal("DefaultFlavorOptions() returned empty slice")
	}

	// Verify each option has name and description
	for i, opt := range options {
		if opt.Name == "" {
			t.Errorf("DefaultFlavorOptions()[%d].Name is empty", i)
		}
		if opt.Description == "" {
			t.Errorf("DefaultFlavorOptions()[%d].Description is empty", i)
		}
	}

	// Verify expected flavors exist
	expectedFlavors := map[string]bool{
		"bookworm":   false,
		"trixie":     false,
		"alpine3.22": false,
		"alpine3.23": false,
	}

	for _, opt := range options {
		if _, ok := expectedFlavors[opt.Name]; ok {
			expectedFlavors[opt.Name] = true
		}
	}

	for name, found := range expectedFlavors {
		if !found {
			t.Errorf("DefaultFlavorOptions() missing expected flavor %q", name)
		}
	}

	// Verify bookworm is first (recommended)
	if options[0].Name != "bookworm" {
		t.Errorf("DefaultFlavorOptions()[0].Name = %q, want %q (should be first/recommended)", options[0].Name, "bookworm")
	}
}

func TestFlavorToImage(t *testing.T) {
	tests := []struct {
		name   string
		flavor string
		want   string
	}{
		{
			name:   "bookworm maps to buildpack-deps",
			flavor: "bookworm",
			want:   "buildpack-deps:bookworm-scm",
		},
		{
			name:   "trixie maps to buildpack-deps",
			flavor: "trixie",
			want:   "buildpack-deps:trixie-scm",
		},
		{
			name:   "alpine3.22 maps to alpine",
			flavor: "alpine3.22",
			want:   "alpine:3.22",
		},
		{
			name:   "alpine3.23 maps to alpine",
			flavor: "alpine3.23",
			want:   "alpine:3.23",
		},
		{
			name:   "custom image passed through as-is",
			flavor: "myregistry/myimage:v1.0",
			want:   "myregistry/myimage:v1.0",
		},
		{
			name:   "unknown flavor passed through",
			flavor: "unknown-flavor",
			want:   "unknown-flavor",
		},
		{
			name:   "empty string passed through",
			flavor: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FlavorToImage(tt.flavor)
			if got != tt.want {
				t.Errorf("FlavorToImage(%q) = %q, want %q", tt.flavor, got, tt.want)
			}
		})
	}
}
