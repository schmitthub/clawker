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
			cfg: &config.Project{
				DefaultImage: "config-image:latest",
			},
			settings: &config.Settings{
				DefaultImage: "settings-image:latest",
			},
			want: "config-image:latest",
		},
		{
			name: "settings fallback when config empty",
			cfg: &config.Project{
				DefaultImage: "",
			},
			settings: &config.Settings{
				DefaultImage: "settings-image:latest",
			},
			want: "settings-image:latest",
		},
		{
			name: "settings only (nil config)",
			cfg:  nil,
			settings: &config.Settings{
				DefaultImage: "settings-only:latest",
			},
			want: "settings-only:latest",
		},
		{
			name: "config only (nil settings)",
			cfg: &config.Project{
				DefaultImage: "config-only:latest",
			},
			settings: nil,
			want:     "config-only:latest",
		},
		{
			name: "both empty returns empty",
			cfg: &config.Project{
				DefaultImage: "",
			},
			settings: &config.Settings{
				DefaultImage: "",
			},
			want: "",
		},
		{
			name: "empty config with nil settings",
			cfg: &config.Project{
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

func TestResolveImage_FallbackToDefault(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Project
		settings *config.Settings
		want     string
	}{
		{
			name: "falls back to config default",
			cfg: &config.Project{
				DefaultImage: "config-default:latest",
			},
			settings: nil,
			want:     "config-default:latest",
		},
		{
			name: "falls back to settings default",
			cfg: &config.Project{
				DefaultImage: "",
			},
			settings: &config.Settings{
				DefaultImage: "settings-default:latest",
			},
			want: "settings-default:latest",
		},
		{
			name: "config default takes precedence over settings",
			cfg: &config.Project{
				DefaultImage: "config-default:latest",
			},
			settings: &config.Settings{
				DefaultImage: "settings-default:latest",
			},
			want: "config-default:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings)
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
	got, err := ResolveImage(context.TODO(), nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveImage() with all nil = %q, want empty string", got)
	}
}

func TestFindProjectImage_NilClient(t *testing.T) {
	ctx := context.Background()

	result, err := FindProjectImage(ctx, nil, "myproject")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string", result)
	}
}

func TestFindProjectImage_EmptyProject(t *testing.T) {
	ctx := context.Background()

	result, err := FindProjectImage(ctx, nil, "")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string", result)
	}
}

func TestResolveImageWithSource_NoDocker(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		cfg        *config.Project
		settings   *config.Settings
		wantRef    string
		wantSource ImageSource
		wantNil    bool
	}{
		{
			name:       "falls back to default from config",
			cfg:        &config.Project{DefaultImage: "config-default:latest"},
			settings:   nil,
			wantRef:    "config-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "falls back to default from settings",
			cfg:        &config.Project{DefaultImage: ""},
			settings:   &config.Settings{DefaultImage: "settings-default:latest"},
			wantRef:    "settings-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "config default takes precedence over settings",
			cfg:        &config.Project{DefaultImage: "config-default:latest"},
			settings:   &config.Settings{DefaultImage: "settings-default:latest"},
			wantRef:    "config-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "returns nil when no image found",
			cfg:        nil,
			settings:   nil,
			wantRef:    "",
			wantSource: "",
			wantNil:    true,
		},
		{
			name: "returns nil when all sources empty",
			cfg: &config.Project{
				DefaultImage: "",
				Project:      "",
			},
			settings: &config.Settings{
				DefaultImage: "",
			},
			wantRef:    "",
			wantSource: "",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveImageWithSource(ctx, nil, tt.cfg, tt.settings)

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
