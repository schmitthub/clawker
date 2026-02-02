package buildkit_test

import (
	"context"
	"os"
	"testing"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/whail/buildkit"
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
