package docker

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

func TestResolveDefaultImage(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Project
		settings config.Settings
		want     string
	}{
		{
			name:     "both zero returns empty",
			cfg:      nil,
			settings: config.Settings{},
			want:     "",
		},
		{
			name: "config takes precedence over settings",
			cfg: &config.Project{
				DefaultImage: "config-image:latest",
			},
			settings: config.Settings{
				DefaultImage: "settings-image:latest",
			},
			want: "config-image:latest",
		},
		{
			name: "settings fallback when config empty",
			cfg: &config.Project{
				DefaultImage: "",
			},
			settings: config.Settings{
				DefaultImage: "settings-image:latest",
			},
			want: "settings-image:latest",
		},
		{
			name: "settings only (nil config)",
			cfg:  nil,
			settings: config.Settings{
				DefaultImage: "settings-only:latest",
			},
			want: "settings-only:latest",
		},
		{
			name: "config only (zero settings)",
			cfg: &config.Project{
				DefaultImage: "config-only:latest",
			},
			settings: config.Settings{},
			want:     "config-only:latest",
		},
		{
			name: "both empty returns empty",
			cfg: &config.Project{
				DefaultImage: "",
			},
			settings: config.Settings{
				DefaultImage: "",
			},
			want: "",
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

func TestResolveImage_FallbackToDefault(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "falls back to config default",
			yaml: `default_image: "config-default:latest"`,
			want: "config-default:latest",
		},
		{
			name: "falls back to settings default",
			yaml: `default_image: "settings-default:latest"`,
			want: "settings-default:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t, tt.yaml)
			client, _ := newTestClientWithConfig(cfg)
			got, err := client.ResolveImage(context.TODO())
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindProjectImage_EmptyProject(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, `project: ""`)
	client, _ := newTestClientWithConfig(cfg)

	result, err := client.findProjectImage(ctx)
	if err != nil {
		t.Errorf("findProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("findProjectImage() = %q, want empty string", result)
	}
}

func TestResolveImageWithSource_NoDocker(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		yaml       string
		wantRef    string
		wantSource ImageSource
		wantNil    bool
	}{
		{
			name:       "falls back to default from config",
			yaml:       `default_image: "config-default:latest"`,
			wantRef:    "config-default:latest",
			wantSource: ImageSourceDefault,
		},
		{
			name:       "falls back to default from settings",
			yaml:       `default_image: "settings-default:latest"`,
			wantRef:    "settings-default:latest",
			wantSource: ImageSourceDefault,
		},
		{
			name:    "returns nil when all sources empty",
			yaml:    `{}`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t, tt.yaml)
			client, _ := newTestClientWithConfig(cfg)

			result, err := client.ResolveImageWithSource(ctx)
			if err != nil {
				t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
			}

			if tt.wantNil {
				if result != nil {
					t.Errorf("ResolveImageWithSource() = %+v, want nil", result)
				}
				return
			}

			if result == nil {
				t.Fatal("ResolveImageWithSource() returned nil, want non-nil")
			}
			if result.Reference != tt.wantRef {
				t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, tt.wantRef)
			}
			if result.Source != tt.wantSource {
				t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, tt.wantSource)
			}
		})
	}
}
