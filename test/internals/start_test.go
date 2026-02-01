package integration

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/cmd/container/start"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// TestStartIntegration_BasicStart tests starting a stopped container.
func TestStartIntegration_BasicStart(t *testing.T) {
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
	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-basic-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-basic-test: %v", err)
		}
	}()

	agentName := "test-start-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create a stopped container (don't start it)
	_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "start-basic-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	// Verify container is not running
	require.False(t, harness.ContainerIsRunning(ctx, rawClient, containerName), "container should be stopped initially")

	// Run start command
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{containerName})

	err = cmd.Execute()
	require.NoError(t, err, "start command failed: stderr=%s", ios.ErrBuf.String())

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, rawClient, containerName)
	require.NoError(t, err, "container did not start")

	// Container is now running - verified by WaitForContainerRunning above
	// Note: The command prints to os.Stdout, not IOStreams, so output checking
	// would require os.Stdout redirection which is messy in tests
}

// TestStartIntegration_BothPatterns tests that both --agent flag and full container name work.
func TestStartIntegration_BothPatterns(t *testing.T) {
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
			rawClient := harness.NewRawDockerClient(t)
			defer rawClient.Close()
			defer func() {
				if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
					t.Logf("WARNING: cleanup failed for %s: %v", project, err)
				}
			}()

			agentName := "test-pattern-" + time.Now().Format("150405.000000")
			containerName := h.ContainerName(agentName)

			// Create a stopped container
			_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
				Name: containerName,
				Config: &container.Config{
					Image: "alpine:latest",
					Cmd:   []string{"sleep", "300"},
					Labels: map[string]string{
						"com.clawker.managed": "true",
						"com.clawker.project": project,
						"com.clawker.agent":   agentName,
					},
				},
			})
			require.NoError(t, err, "failed to create container")

			// Run start command with appropriate args
			ios := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

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
			err = harness.WaitForContainerRunning(readyCtx, rawClient, containerName)
			require.NoError(t, err, "container did not start")
		})
	}
}

// TestStartIntegration_BothImages tests that both Alpine and Debian images start correctly.
func TestStartIntegration_BothImages(t *testing.T) {
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
			rawClient := harness.NewRawDockerClient(t)
			defer rawClient.Close()
			defer func() {
				if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
					t.Logf("WARNING: cleanup failed for %s: %v", project, err)
				}
			}()

			agentName := "test-image-" + time.Now().Format("150405.000000")
			containerName := h.ContainerName(agentName)

			// Pull image if not present
			reader, pullErr := rawClient.ImagePull(ctx, tt.image, client.ImagePullOptions{})
			if pullErr == nil {
				defer reader.Close()
				_, _ = io.Copy(io.Discard, reader) // Wait for pull to complete
			}
			// Ignore pull errors - the create will fail if image truly not available

			// Create a stopped container with specific image
			_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
				Name: containerName,
				Config: &container.Config{
					Image: tt.image,
					Cmd:   []string{"sleep", "300"},
					Labels: map[string]string{
						"com.clawker.managed": "true",
						"com.clawker.project": project,
						"com.clawker.agent":   agentName,
					},
				},
			})
			require.NoError(t, err, "failed to create container with image %s", tt.image)

			// Run start command
			ios := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			cmd := start.NewCmdStart(f, nil)
			cmd.SetArgs([]string{containerName})

			err = cmd.Execute()
			require.NoError(t, err, "start command failed for %s: stderr=%s", tt.image, ios.ErrBuf.String())

			// Wait for container to be running
			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			err = harness.WaitForContainerRunning(readyCtx, rawClient, containerName)
			require.NoError(t, err, "container did not start for image %s", tt.image)
		})
	}
}

// TestStartIntegration_MultipleContainers tests starting multiple containers at once.
func TestStartIntegration_MultipleContainers(t *testing.T) {
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
	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()
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

	// Create 3 stopped containers
	for i, name := range containerNames {
		agentName := "agent" + string(rune('1'+i)) + "-" + timestamp
		_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					"com.clawker.managed": "true",
					"com.clawker.project": "start-multi-test",
					"com.clawker.agent":   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container %s", name)
	}

	// Run start command with all 3 containers
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs(containerNames)

	err := cmd.Execute()
	require.NoError(t, err, "start command failed: stderr=%s", ios.ErrBuf.String())

	// Wait for all containers to be running
	for _, name := range containerNames {
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = harness.WaitForContainerRunning(readyCtx, rawClient, name)
		cancel()
		require.NoError(t, err, "container %s did not start", name)
	}

	// All containers are now running - verified by WaitForContainerRunning loop above
	// Note: The command prints to os.Stdout, not IOStreams, so output checking
	// would require os.Stdout redirection which is messy in tests
}

// TestStartIntegration_AlreadyRunning tests that starting an already-running container is idempotent.
func TestStartIntegration_AlreadyRunning(t *testing.T) {
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
	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "start-running-test"); err != nil {
			t.Logf("WARNING: cleanup failed for start-running-test: %v", err)
		}
	}()

	agentName := "test-running-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "start-running-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, rawClient, containerName)
	require.NoError(t, err, "container did not start")

	// Try to start it again - should succeed (idempotent)
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{containerName})

	err = cmd.Execute()
	require.NoError(t, err, "start command should succeed for already-running container: stderr=%s", ios.ErrBuf.String())

	// Container should still be running
	require.True(t, harness.ContainerIsRunning(ctx, rawClient, containerName), "container should still be running")
}

// TestStartIntegration_NonExistent tests that starting a non-existent container returns an error.
func TestStartIntegration_NonExistent(t *testing.T) {
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
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs([]string{"clawker.start-nonexist-test.doesnotexist"})

	err := cmd.Execute()
	require.Error(t, err, "start command should fail for non-existent container")
}

// TestStartIntegration_MultipleWithAttach tests that using --attach with multiple containers returns an error.
func TestStartIntegration_MultipleWithAttach(t *testing.T) {
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
	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()
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

	// Create 2 stopped containers
	for i, name := range containerNames {
		agentName := "attach" + string(rune('1'+i)) + "-" + timestamp
		_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					"com.clawker.managed": "true",
					"com.clawker.project": "start-attach-test",
					"com.clawker.agent":   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container %s", name)
	}

	// Try to start with --attach and multiple containers
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := start.NewCmdStart(f, nil)
	cmd.SetArgs(append([]string{"--attach"}, containerNames...))

	err := cmd.Execute()
	require.Error(t, err, "start with --attach and multiple containers should fail")
	require.Contains(t, err.Error(), "cannot attach to multiple containers", "error should mention cannot attach to multiple")
}
