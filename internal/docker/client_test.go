package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

func TestParseContainers(t *testing.T) {
	tests := []struct {
		name string
		in   []container.Summary
		want []Container
	}{
		{
			name: "empty list",
			in:   []container.Summary{},
			want: []Container{},
		},
		{
			name: "single container with all labels",
			in: []container.Summary{
				{
					ID:    "abc123",
					Names: []string{"/clawker.myapp.dev"},
					Labels: map[string]string{
						LabelProject: "myapp",
						LabelAgent:   "dev",
						LabelImage:   "node:20",
						LabelWorkdir: "/workspace",
					},
					State:   "running",
					Created: 1700000000,
				},
			},
			want: []Container{
				{
					ID:      "abc123",
					Name:    "clawker.myapp.dev",
					Project: "myapp",
					Agent:   "dev",
					Image:   "node:20",
					Workdir: "/workspace",
					Status:  "running",
					Created: 1700000000,
				},
			},
		},
		{
			name: "multiple containers",
			in: []container.Summary{
				{
					ID:    "aaa",
					Names: []string{"/clawker.proj.agent1"},
					Labels: map[string]string{
						LabelProject: "proj",
						LabelAgent:   "agent1",
					},
					State:   "running",
					Created: 100,
				},
				{
					ID:    "bbb",
					Names: []string{"/clawker.proj.agent2"},
					Labels: map[string]string{
						LabelProject: "proj",
						LabelAgent:   "agent2",
					},
					State:   "exited",
					Created: 200,
				},
			},
			want: []Container{
				{
					ID:      "aaa",
					Name:    "clawker.proj.agent1",
					Project: "proj",
					Agent:   "agent1",
					Status:  "running",
					Created: 100,
				},
				{
					ID:      "bbb",
					Name:    "clawker.proj.agent2",
					Project: "proj",
					Agent:   "agent2",
					Status:  "exited",
					Created: 200,
				},
			},
		},
		{
			name: "missing labels falls back to Docker image",
			in: []container.Summary{
				{
					ID:      "ccc",
					Names:   []string{"/some-container"},
					Image:   "alpine:latest",
					Labels:  map[string]string{},
					State:   "created",
					Created: 300,
				},
			},
			want: []Container{
				{
					ID:      "ccc",
					Name:    "some-container",
					Project: "",
					Agent:   "",
					Image:   "alpine:latest",
					Workdir: "",
					Status:  "created",
					Created: 300,
				},
			},
		},
		{
			name: "label image takes precedence over Docker image",
			in: []container.Summary{
				{
					ID:    "fff",
					Names: []string{"/clawker.test.agent"},
					Image: "sha256:abc123def456",
					Labels: map[string]string{
						LabelProject: "test",
						LabelAgent:   "agent",
						LabelImage:   "clawker-test:latest",
					},
					State:   "running",
					Created: 400,
				},
			},
			want: []Container{
				{
					ID:      "fff",
					Name:    "clawker.test.agent",
					Project: "test",
					Agent:   "agent",
					Image:   "clawker-test:latest",
					Status:  "running",
					Created: 400,
				},
			},
		},
		{
			name: "name without leading slash",
			in: []container.Summary{
				{
					ID:     "ddd",
					Names:  []string{"no-slash"},
					Labels: map[string]string{},
					State:  "running",
				},
			},
			want: []Container{
				{
					ID:     "ddd",
					Name:   "no-slash",
					Status: "running",
				},
			},
		},
		{
			name: "no names at all",
			in: []container.Summary{
				{
					ID:     "eee",
					Names:  []string{},
					Labels: map[string]string{},
					State:  "running",
				},
			},
			want: []Container{
				{
					ID:     "eee",
					Name:   "",
					Status: "running",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseContainers(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseContainers() returned %d containers, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("container[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "DockerError with not found message",
			err:  &whail.DockerError{Op: "inspect", Message: "container not found"},
			want: true,
		},
		{
			name: "DockerError with No such message",
			err:  &whail.DockerError{Op: "inspect", Message: "No such container: abc123"},
			want: true,
		},
		{
			name: "DockerError with unrelated message",
			err:  &whail.DockerError{Op: "build", Message: "permission denied"},
			want: false,
		},
		{
			name: "raw error with not found",
			err:  fmt.Errorf("container not found"),
			want: true,
		},
		{
			name: "raw error with No such",
			err:  fmt.Errorf("No such image: foo"),
			want: true,
		},
		{
			name: "raw error unrelated",
			err:  fmt.Errorf("connection refused"),
			want: false,
		},
		{
			name: "wrapped DockerError with not found",
			err:  fmt.Errorf("operation failed: %w", &whail.DockerError{Op: "remove", Message: "not found"}),
			want: true,
		},
		{
			name: "wrapped raw error with not found",
			err:  fmt.Errorf("cleanup: %w", errors.New("volume not found")),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundError(tt.err)
			if got != tt.want {
				t.Errorf("isNotFoundError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// clawkerEngine returns a whail.Engine with clawker's production label config
// backed by the given FakeAPIClient.
func clawkerEngine(fake *whailtest.FakeAPIClient) *whail.Engine {
	return whail.NewFromExisting(fake, whail.EngineOptions{
		LabelPrefix:  EngineLabelPrefix,
		ManagedLabel: EngineManagedLabel,
	})
}

func TestBuildImage_RoutesToBuildKit(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := clawkerEngine(fake)
	client := &Client{Engine: engine}

	capture := &whailtest.BuildKitCapture{}
	engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)

	ctx := context.Background()
	err := client.BuildImage(ctx, bytes.NewReader(nil), BuildImageOpts{
		Tags:            []string{"test:latest"},
		BuildKitEnabled: true,
		ContextDir:      "/tmp/build",
		SuppressOutput:  true,
		Labels:          map[string]string{"app": "myapp"},
	})
	if err != nil {
		t.Fatalf("BuildImage() error: %v", err)
	}

	if capture.CallCount != 1 {
		t.Fatalf("expected BuildKit builder to be called once, got %d", capture.CallCount)
	}
	if capture.Opts.ContextDir != "/tmp/build" {
		t.Errorf("expected ContextDir %q, got %q", "/tmp/build", capture.Opts.ContextDir)
	}
	if capture.Opts.Tags[0] != "test:latest" {
		t.Errorf("expected tag %q, got %q", "test:latest", capture.Opts.Tags[0])
	}

	// Verify managed label was injected by whail
	managedKey := EngineLabelPrefix + "." + EngineManagedLabel
	if capture.Opts.Labels[managedKey] != "true" {
		t.Errorf("expected managed label %q=true, got %q", managedKey, capture.Opts.Labels[managedKey])
	}

	// Legacy ImageBuild should NOT have been called
	whailtest.AssertNotCalled(t, fake, "ImageBuild")
}

func TestBuildImage_RoutesToLegacy(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := clawkerEngine(fake)
	client := &Client{Engine: engine}

	// Wire BuildKit to verify it's NOT called
	capture := &whailtest.BuildKitCapture{}
	engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)

	// Wire legacy ImageBuild to return an empty (valid) response
	fake.ImageBuildFn = func(_ context.Context, _ io.Reader, _ moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		return moby.ImageBuildResult{
			Body: io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	ctx := context.Background()
	err := client.BuildImage(ctx, bytes.NewReader(nil), BuildImageOpts{
		Tags:            []string{"test:latest"},
		BuildKitEnabled: false, // legacy path
		SuppressOutput:  true,
	})
	if err != nil {
		t.Fatalf("BuildImage() error: %v", err)
	}

	// BuildKit should NOT have been called
	if capture.CallCount != 0 {
		t.Errorf("expected BuildKit builder to NOT be called, got %d calls", capture.CallCount)
	}

	// Legacy ImageBuild should have been called
	whailtest.AssertCalled(t, fake, "ImageBuild")
}

func TestBuildImage_BuildKitWithoutContextDir_FallsToLegacy(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := clawkerEngine(fake)
	client := &Client{Engine: engine}

	capture := &whailtest.BuildKitCapture{}
	engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)

	fake.ImageBuildFn = func(_ context.Context, _ io.Reader, _ moby.ImageBuildOptions) (moby.ImageBuildResult, error) {
		return moby.ImageBuildResult{
			Body: io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	ctx := context.Background()
	err := client.BuildImage(ctx, bytes.NewReader(nil), BuildImageOpts{
		Tags:            []string{"test:latest"},
		BuildKitEnabled: true,
		ContextDir:      "", // empty — should fall to legacy
		SuppressOutput:  true,
	})
	if err != nil {
		t.Fatalf("BuildImage() error: %v", err)
	}

	if capture.CallCount != 0 {
		t.Errorf("expected BuildKit builder to NOT be called when ContextDir is empty, got %d calls", capture.CallCount)
	}
	whailtest.AssertCalled(t, fake, "ImageBuild")
}
