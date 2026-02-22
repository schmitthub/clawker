package docker

import (
	"context"
	"testing"

	moby "github.com/moby/moby/client"
)

func TestFindProjectImage_EmptyProject(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, `{}`)
	client, _ := newTestClientWithConfig(cfg)

	// Empty projectName returns empty string immediately (no Docker call)
	result, err := client.findProjectImage(ctx, "")
	if err != nil {
		t.Errorf("findProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("findProjectImage() = %q, want empty string", result)
	}
}

func TestResolveImageWithSource_ProjectOnly(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		projectName string
		wantNil     bool
	}{
		{
			name:        "returns nil for empty project name",
			projectName: "",
			wantNil:     true,
		},
		{
			name:        "returns nil when no project image found",
			projectName: "myproject",
			wantNil:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t, `{}`)
			client, fakeAPI := newTestClientWithConfig(cfg)

			// Wire ImageList to return empty results
			fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
				return moby.ImageListResult{}, nil
			}

			result, err := client.ResolveImageWithSource(ctx, tt.projectName)
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
		})
	}
}

func TestResolveImage_EmptyProject(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t, `{}`)
	client, fakeAPI := newTestClientWithConfig(cfg)

	// Wire ImageList to return empty results
	fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
		return moby.ImageListResult{}, nil
	}

	got, err := client.ResolveImage(ctx, "")
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveImage() = %q, want empty string", got)
	}
}
