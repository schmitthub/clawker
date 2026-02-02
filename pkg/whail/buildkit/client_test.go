package buildkit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/buildkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBuildKitClient_Integration verifies that we can connect to Docker's
// embedded BuildKit daemon via the /grpc and /session hijack endpoints.
// Requires a running Docker daemon with BuildKit support.
func TestNewBuildKitClient_Integration(t *testing.T) {
	if os.Getenv("CLAWKER_INTEGRATION") == "" {
		t.Skip("skipping integration test: set CLAWKER_INTEGRATION=1 to run")
	}

	ctx := context.Background()

	// Create a real Docker client
	dockerClient, err := client.New(client.FromEnv)
	require.NoError(t, err, "failed to create docker client")
	defer dockerClient.Close()

	// Verify Docker is reachable
	_, err = dockerClient.Ping(ctx, client.PingOptions{})
	require.NoError(t, err, "docker daemon not reachable")

	// Create BuildKit client via hijack endpoints
	bkClient, err := buildkit.NewBuildKitClient(ctx, dockerClient)
	require.NoError(t, err, "failed to create BuildKit client")
	defer bkClient.Close()

	// Verify the connection works and workers are available
	err = buildkit.VerifyConnection(ctx, bkClient)
	require.NoError(t, err, "BuildKit connection verification failed")
}

// TestImageBuildKit_Integration verifies the full BuildKit build flow via
// Engine.ImageBuildKit with closure injection, including label enforcement.
// Requires a running Docker daemon with BuildKit support.
func TestImageBuildKit_Integration(t *testing.T) {
	if os.Getenv("CLAWKER_INTEGRATION") == "" {
		t.Skip("skipping integration test: set CLAWKER_INTEGRATION=1 to run")
	}

	ctx := context.Background()
	tag := "whail-buildkit-integration-test:latest"

	// Create engine with BuildKit wired
	engine, err := whail.New(ctx)
	require.NoError(t, err)
	engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)

	// Cleanup: remove test image after test
	t.Cleanup(func() {
		_, _ = engine.ImageRemove(context.Background(), tag, whail.ImageRemoveOptions{Force: true})
	})

	// Write minimal Dockerfile to temp dir
	dir := t.TempDir()
	err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:latest\nCMD [\"echo\", \"hello\"]\n"), 0644)
	require.NoError(t, err)

	// Build via BuildKit
	err = engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
		Tags:           []string{tag},
		ContextDir:     dir,
		SuppressOutput: true,
		Labels: map[string]string{
			"test.label": "integration",
		},
	})
	require.NoError(t, err, "ImageBuildKit failed")

	// Verify image exists and has managed labels
	images, err := engine.ImageList(ctx, whail.ImageListOptions{})
	require.NoError(t, err)

	found := false
	for _, img := range images.Items {
		for _, t := range img.RepoTags {
			if t == tag {
				found = true
				break
			}
		}
	}
	assert.True(t, found, "built image %q not found in managed image list", tag)
}
