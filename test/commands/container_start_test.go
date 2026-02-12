package commands

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmd/container/start"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// TestContainerStart_BasicStart tests starting a stopped container.
func TestContainerStart_BasicStart(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("start-basic-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-basic-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-basic-test: %v", err)
		}
	}()

	agentName := "test-start-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create a stopped container (don't start it) — whail auto-injects managed + test labels
	_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: "start-basic-test",
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	// Verify container is not running
	require.False(t, harness.ContainerIsRunning(ctx, dockerClient, containerName), "container should be stopped initially")

	// Run start command
	f, ios := harness.NewTestFactory(t, h)

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{containerName})

	err = cmd.Execute()
	require.NoError(t, err, "start command failed: stderr=%s", ios.ErrBuf.String())

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, containerName)
	require.NoError(t, err, "container did not start")

	// Container is now running - verified by WaitForContainerRunning above
	// Note: The command prints to os.Stdout, not IOStreams, so output checking
	// would require os.Stdout redirection which is messy in tests
}

// TestContainerStart_BothPatterns tests that both --agent flag and full container name work.
func TestContainerStart_BothPatterns(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		useAgent bool
	}{
		{"with_agent_flag", true},
		{"with_container_name", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := "start-pattern-" + tt.name
			h := harness.NewHarness(t,
				harness.WithConfigBuilder(
					builders.MinimalValidConfig().
						WithProject(project).
						WithSecurity(builders.SecurityFirewallDisabled()),
				),
			)
			h.Chdir()

			dockerClient := harness.NewTestClient(t)
			defer func() {
				if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
					t.Logf("WARNING: cleanup failed for %s: %v", project, err)
				}
			}()

			agentName := "test-pattern-" + time.Now().Format("150405.000000")
			containerName := h.ContainerName(agentName)

			// Create a stopped container — whail auto-injects managed + test labels
			_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
				Name: containerName,
				Config: &container.Config{
					Image: "alpine:latest",
					Cmd:   []string{"sleep", "300"},
					Labels: map[string]string{
						docker.LabelProject: project,
						docker.LabelAgent:   agentName,
					},
				},
			})
			require.NoError(t, err, "failed to create container")

			// Run start command with appropriate args
			f, ios := harness.NewTestFactory(t, h)

			cmd := start.NewCmdStart(f, nil)
			if tt.useAgent {
				cmd.SetArgs([]string{"--agent", agentName})
			} else {
				cmd.SetArgs([]string{containerName})
			}

			err = cmd.Execute()
			require.NoError(t, err, "start command failed: stderr=%s", ios.ErrBuf.String())

			// Wait for container to be running
			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			err = harness.WaitForContainerRunning(readyCtx, dockerClient, containerName)
			require.NoError(t, err, "container did not start")
		})
	}
}

// TestContainerStart_BothImages tests that both Alpine and Debian images start correctly.
func TestContainerStart_BothImages(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		image string
	}{
		{"alpine", "alpine:latest"},
		{"busybox", "busybox:latest"}, // Use busybox as a lightweight alternative to debian
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := "start-image-" + tt.name
			h := harness.NewHarness(t,
				harness.WithConfigBuilder(
					builders.MinimalValidConfig().
						WithProject(project).
						WithSecurity(builders.SecurityFirewallDisabled()),
				),
			)
			h.Chdir()

			dockerClient := harness.NewTestClient(t)
			defer func() {
				if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
					t.Logf("WARNING: cleanup failed for %s: %v", project, err)
				}
			}()

			agentName := "test-image-" + time.Now().Format("150405.000000")
			containerName := h.ContainerName(agentName)

			// Pull image if not present
			reader, pullErr := dockerClient.ImagePull(ctx, tt.image, docker.ImagePullOptions{})
			if pullErr == nil {
				defer reader.Close()
				_, _ = io.Copy(io.Discard, reader) // Wait for pull to complete
			}
			// Ignore pull errors - the create will fail if image truly not available

			// Create a stopped container with specific image — whail auto-injects managed + test labels
			_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
				Name: containerName,
				Config: &container.Config{
					Image: tt.image,
					Cmd:   []string{"sleep", "300"},
					Labels: map[string]string{
						docker.LabelProject: project,
						docker.LabelAgent:   agentName,
					},
				},
			})
			require.NoError(t, err, "failed to create container with image %s", tt.image)

			// Run start command
			f, ios := harness.NewTestFactory(t, h)

			cmd := start.NewCmdStart(f, nil)
			cmd.SetArgs([]string{containerName})

			err = cmd.Execute()
			require.NoError(t, err, "start command failed for %s: stderr=%s", tt.image, ios.ErrBuf.String())

			// Wait for container to be running
			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			err = harness.WaitForContainerRunning(readyCtx, dockerClient, containerName)
			require.NoError(t, err, "container did not start for image %s", tt.image)
		})
	}
}

// TestContainerStart_MultipleContainers tests starting multiple containers at once.
func TestContainerStart_MultipleContainers(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("start-multi-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-multi-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-multi-test: %v", err)
		}
	}()

	timestamp := time.Now().Format("150405.000000")
	containerNames := []string{
		h.ContainerName("agent1-" + timestamp),
		h.ContainerName("agent2-" + timestamp),
		h.ContainerName("agent3-" + timestamp),
	}

	// Create 3 stopped containers — whail auto-injects managed + test labels
	for i, name := range containerNames {
		agentName := "agent" + string(rune('1'+i)) + "-" + timestamp
		_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					docker.LabelProject: "start-multi-test",
					docker.LabelAgent:   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container %s", name)
	}

	// Run start command with all 3 containers
	f, ios := harness.NewTestFactory(t, h)

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs(containerNames)

	err := cmd.Execute()
	require.NoError(t, err, "start command failed: stderr=%s", ios.ErrBuf.String())

	// Wait for all containers to be running
	for _, name := range containerNames {
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = harness.WaitForContainerRunning(readyCtx, dockerClient, name)
		cancel()
		require.NoError(t, err, "container %s did not start", name)
	}

	// All containers are now running - verified by WaitForContainerRunning loop above
	// Note: The command prints to os.Stdout, not IOStreams, so output checking
	// would require os.Stdout redirection which is messy in tests
}

// TestContainerStart_AlreadyRunning tests that starting an already-running container is idempotent.
func TestContainerStart_AlreadyRunning(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("start-running-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-running-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-running-test: %v", err)
		}
	}()

	agentName := "test-running-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container — whail auto-injects managed + test labels
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: "start-running-test",
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, containerName)
	require.NoError(t, err, "container did not start")

	// Try to start it again - should succeed (idempotent)
	f, ios := harness.NewTestFactory(t, h)

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{containerName})

	err = cmd.Execute()
	require.NoError(t, err, "start command should succeed for already-running container: stderr=%s", ios.ErrBuf.String())

	// Container should still be running
	require.True(t, harness.ContainerIsRunning(ctx, dockerClient, containerName), "container should still be running")
}

// TestContainerStart_NonExistent tests that starting a non-existent container returns an error.
func TestContainerStart_NonExistent(t *testing.T) {
	harness.RequireDocker(t)

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("start-nonexist-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Try to start a container that doesn't exist
	f, _ := harness.NewTestFactory(t, h)

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.start-nonexist-test.doesnotexist"})

	err := cmd.Execute()
	require.Error(t, err, "start command should fail for non-existent container")
}

// TestContainerStart_MultipleWithAttach tests that using --attach with multiple containers returns an error.
func TestContainerStart_MultipleWithAttach(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("start-attach-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-attach-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-attach-test: %v", err)
		}
	}()

	timestamp := time.Now().Format("150405.000000")
	containerNames := []string{
		h.ContainerName("attach1-" + timestamp),
		h.ContainerName("attach2-" + timestamp),
	}

	// Create 2 stopped containers — whail auto-injects managed + test labels
	for i, name := range containerNames {
		agentName := "attach" + string(rune('1'+i)) + "-" + timestamp
		_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					docker.LabelProject: "start-attach-test",
					docker.LabelAgent:   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container %s", name)
	}

	// Try to start with --attach and multiple containers
	f, _ := harness.NewTestFactory(t, h)

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs(append([]string{"--attach"}, containerNames...))

	err := cmd.Execute()
	require.Error(t, err, "start with --attach and multiple containers should fail")
	require.Contains(t, err.Error(), "cannot attach to multiple containers", "error should mention cannot attach to multiple")
}
