package cmdutil

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

func TestResolveDefaultImage(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name:     "both nil returns empty",
			cfg:      nil,
			settings: nil,
			want:     "",
		},
		{
			name: "config takes precedence over settings",
			cfg: &config.Config{
				DefaultImage: "config-image:latest",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "config-image:latest",
		},
		{
			name: "settings fallback when config empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "settings-image:latest",
		},
		{
			name:     "settings only (nil config)",
			cfg:      nil,
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-only:latest",
				},
			},
			want: "settings-only:latest",
		},
		{
			name: "config only (nil settings)",
			cfg: &config.Config{
				DefaultImage: "config-only:latest",
			},
			settings: nil,
			want:     "config-only:latest",
		},
		{
			name: "both empty returns empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "",
				},
			},
			want: "",
		},
		{
			name: "empty config with nil settings",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDefaultImage(tt.cfg, tt.settings)
			if got != tt.want {
				t.Errorf("ResolveDefaultImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_ExplicitImage(t *testing.T) {
	// When explicit image is provided, it should always be returned
	tests := []struct {
		name          string
		cfg           *config.Config
		settings      *config.Settings
		explicitImage string
		want          string
	}{
		{
			name:          "explicit image takes precedence over all",
			cfg:           &config.Config{DefaultImage: "config-image:latest"},
			settings:      &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-image:latest"}},
			explicitImage: "explicit:v1",
			want:          "explicit:v1",
		},
		{
			name:          "explicit image with nil config and settings",
			cfg:           nil,
			settings:      nil,
			explicitImage: "explicit:v2",
			want:          "explicit:v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We pass nil for dockerClient since we're testing with explicit image
			// which doesn't need Docker lookup
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings, tt.explicitImage)
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_FallbackToDefault(t *testing.T) {
	// When no explicit image, should fall back to default from config/settings
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name: "falls back to config default",
			cfg: &config.Config{
				DefaultImage: "config-default:latest",
			},
			settings: nil,
			want:     "config-default:latest",
		},
		{
			name: "falls back to settings default",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-default:latest",
				},
			},
			want: "settings-default:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings, "")
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_NilParameters(t *testing.T) {
	// Test with all nil parameters - should return empty string, no error
	got, err := ResolveImage(context.TODO(), nil, nil, nil, "")
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveImage() with all nil = %q, want empty string", got)
	}
}

func TestResolveImage_EmptyExplicitUsesDefaults(t *testing.T) {
	cfg := &config.Config{
		DefaultImage: "default-image:v1",
	}

	// Empty explicit image should use default
	got, err := ResolveImage(context.TODO(), nil, cfg, nil, "")
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "default-image:v1" {
		t.Errorf("ResolveImage() = %q, want %q", got, "default-image:v1")
	}
}
