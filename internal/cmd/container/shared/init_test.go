package shared

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/volume"
	moby "github.com/moby/moby/client"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testNotFoundError satisfies errdefs.IsNotFound for test fakes.
type testNotFoundError struct{ msg string }

func (e testNotFoundError) Error() string { return e.msg }
func (e testNotFoundError) NotFound()     {}

// testInitializer creates a ContainerInitializer with test defaults.
func testInitializer(ios *iostreams.IOStreams) *ContainerInitializer {
	return &ContainerInitializer{
		ios: ios,
		tui: tui.NewTUI(ios),
		gitMgr: func() (*git.GitManager, error) {
			return nil, fmt.Errorf("GitManager not available in test")
		},
		hostProxy: func() *hostproxy.Manager {
			return hostproxy.NewManager()
		},
	}
}

// testConfig returns a minimal *config.Project for init tests.
func testConfig() *config.Project {
	hostProxyDisabled := false
	return &config.Project{
		Version: "1",
		Project: "testproject",
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{
			EnableHostProxy: &hostProxyDisabled,
			Firewall: &config.FirewallConfig{
				Enable: false,
			},
		},
	}
}

// testFlags returns a FlagSet from a minimal cobra command with container flags registered.
func testFlags() *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	cmd := &cobra.Command{Use: "test"}
	copts.AddFlags(cmd.Flags(), containerOpts)
	return cmd
}

func TestContainerInitializer_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
		StartAfterCreate: false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
	require.Equal(t, "test-agent", result.AgentName)
	require.Equal(t, "clawker.testproject.test-agent", result.ContainerName)
	fake.AssertCalled(t, "ContainerCreate")
}

func TestContainerInitializer_StartAfterCreate(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()
	fake.SetupContainerStart()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "worker"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
		StartAfterCreate: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
	fake.AssertCalled(t, "ContainerCreate")
	fake.AssertCalled(t, "ContainerStart")
}

func TestContainerInitializer_ContainerCreateError(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
	}

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")
	require.Nil(t, result)
}

func TestContainerInitializer_ConfigCached(t *testing.T) {
	// Default fake: volumes exist → ConfigCreated=false → config step is cached
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
}

func TestContainerInitializer_ConfigFresh(t *testing.T) {
	// Volumes don't exist → EnsureVolume creates → ConfigCreated=true → init runs
	fake := dockertest.NewFakeClient()
	fake.SetupVolumeExists("", false)
	fake.FakeAPI.VolumeCreateFn = func(_ context.Context, _ moby.VolumeCreateOptions) (moby.VolumeCreateResult, error) {
		return moby.VolumeCreateResult{}, nil
	}
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	// Point CLAUDE_CONFIG_DIR to non-existent path so InitContainerConfig fails
	// (proving it WAS called when ConfigCreated=true)
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-init-test-dir")

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")
}

func TestContainerInitializer_HostProxyFailure(t *testing.T) {
	// Host proxy enabled in config, but proxy manager fails — non-fatal, continues with warning
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	hostProxyEnabled := true
	cfg.Security.EnableHostProxy = &hostProxyEnabled

	tio := iostreams.NewTestIOStreams()
	ci := &ContainerInitializer{
		ios: tio.IOStreams,
		tui: tui.NewTUI(tio.IOStreams),
		gitMgr: func() (*git.GitManager, error) {
			return nil, fmt.Errorf("GitManager not available in test")
		},
		hostProxy: func() *hostproxy.Manager {
			// Use port 0 so EnsureRunning always fails regardless of host state
			// (no daemon on port 0, startDaemon spawns test binary which exits immediately)
			return hostproxy.NewManagerWithOptions(0, "")
		},
	}

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	// Should succeed despite host proxy failure (non-fatal)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.HostProxyRunning, "host proxy should not be running")
	require.NotEmpty(t, result.ContainerID)
}

func TestContainerInitializer_ContainerStartError(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()
	fake.FakeAPI.ContainerStartFn = func(_ context.Context, _ string, _ moby.ContainerStartOptions) (moby.ContainerStartResult, error) {
		return moby.ContainerStartResult{}, fmt.Errorf("port already in use")
	}

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
		StartAfterCreate: true,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "starting container")
	require.Nil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
}

func TestContainerInitializer_OnboardingSkippedWhenDisabled(t *testing.T) {
	// UseHostAuth=false → no onboarding injection → CopyToContainer not called
	cfg := testConfig()
	useHostAuth := false
	cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
		UseHostAuth: &useHostAuth,
		Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
	}

	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	// No CopyToContainer setup — if called, would panic

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	fake.AssertNotCalled(t, "CopyToContainer")
}

func TestContainerInitializer_PostInit(t *testing.T) {
	// PostInit configured → CopyToContainer called for post-init script injection
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.PostInit = "npm install -g typescript\n"

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	// CopyToContainer should be called — for onboarding + post-init
	fake.AssertCalled(t, "CopyToContainer")
	// Verify it was called at least twice (onboarding + post-init)
	fake.AssertCalledN(t, "CopyToContainer", 2)
}

func TestContainerInitializer_NoPostInit(t *testing.T) {
	// No PostInit configured, no host auth → CopyToContainer not called
	cfg := testConfig()
	useHostAuth := false
	cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
		UseHostAuth: &useHostAuth,
		Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
	}

	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	// No CopyToContainer setup — if called, would fail

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	fake.AssertNotCalled(t, "CopyToContainer")
}

func TestContainerInitializer_PostInitInjectionError(t *testing.T) {
	// PostInit configured but CopyToContainer fails on the second call (post-init injection).
	// First call (onboarding) succeeds, second call (post-init) fails.
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()

	// Custom CopyToContainer: succeed first (onboarding), fail second (post-init)
	callCount := 0
	fake.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ moby.CopyToContainerOptions) (moby.CopyToContainerResult, error) {
		callCount++
		if callCount >= 2 {
			return moby.CopyToContainerResult{}, fmt.Errorf("simulated copy failure")
		}
		return moby.CopyToContainerResult{}, nil
	}

	cfg := testConfig()
	cfg.Agent.PostInit = "npm install -g typescript\n"

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "inject post-init script")
	require.Nil(t, result)
}

func TestContainerInitializer_EmptyProject(t *testing.T) {
	// Empty project → 2-segment container name (clawker.agent)
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Project = "" // empty project

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "myagent"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "myagent", result.AgentName)
	require.Equal(t, "clawker.myagent", result.ContainerName)
}

func TestContainerInitializer_EnvFileError(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cfg := testConfig()
	cfg.Agent.EnvFile = []string{"/nonexistent/file.env"}

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent.env_file")
	fake.AssertNotCalled(t, "ContainerCreate")
}

func TestContainerInitializer_FromEnvWarnings(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cfg := testConfig()
	cfg.Agent.FromEnv = []string{"CLAWKER_NONEXISTENT_VAR_99999"}

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Warnings)
	require.Contains(t, result.Warnings[0], "CLAWKER_NONEXISTENT_VAR_99999")
}

func TestContainerInitializer_RandomAgentName(t *testing.T) {
	// No agent specified → random name generated
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	// No agent or name set

	result, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.AgentName, "should have generated a random agent name")
}

func TestContainerInitializer_CleanupVolumesOnCreateError(t *testing.T) {
	// When volumes are freshly created and a subsequent init step fails,
	// deferred cleanup removes newly-created volumes.
	fake := dockertest.NewFakeClient()

	// Track which volumes have been "created" — allows VolumeInspect to return
	// "not found" initially (so EnsureVolume creates) then "managed" (so VolumeRemove works).
	createdVols := map[string]bool{}
	fake.FakeAPI.VolumeInspectFn = func(_ context.Context, id string, _ moby.VolumeInspectOptions) (moby.VolumeInspectResult, error) {
		if createdVols[id] {
			return moby.VolumeInspectResult{
				Volume: volume.Volume{
					Name:   id,
					Labels: map[string]string{docker.LabelManaged: docker.ManagedLabelValue},
				},
			}, nil
		}
		return moby.VolumeInspectResult{}, testNotFoundError{msg: "No such volume: " + id}
	}
	fake.FakeAPI.VolumeCreateFn = func(_ context.Context, opts moby.VolumeCreateOptions) (moby.VolumeCreateResult, error) {
		createdVols[opts.Name] = true
		return moby.VolumeCreateResult{}, nil
	}

	// VolumeRemove should be called for cleanup
	var removedVolumes []string
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, volumeID string, _ moby.VolumeRemoveOptions) (moby.VolumeRemoveResult, error) {
		removedVolumes = append(removedVolumes, volumeID)
		return moby.VolumeRemoveResult{}, nil
	}

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	// Config init will fail because CLAUDE_CONFIG_DIR is invalid.
	// This happens AFTER volumes are created but BEFORE ContainerCreate,
	// which triggers the deferred cleanup of those newly-created volumes.
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-cleanup-test-dir")

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")

	// Verify volumes were cleaned up
	require.NotEmpty(t, removedVolumes, "should have cleaned up newly-created volumes")
	require.NotEmpty(t, ci.CleanupWarnings(), "should have cleanup warnings")
}

func TestContainerInitializer_NoCleanupForPreExistingVolumes(t *testing.T) {
	// When volumes already exist (ConfigCreated=false), no cleanup on failure.
	fake := dockertest.NewFakeClient()
	// Default: VolumeExists returns true → no volumes created

	// ContainerCreate fails
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		return moby.ContainerCreateResult{}, fmt.Errorf("image not found")
	}

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")

	// No VolumeRemove calls — volumes were pre-existing
	fake.AssertNotCalled(t, "VolumeRemove")
	require.Empty(t, ci.CleanupWarnings(), "should have no cleanup warnings")
}

func TestContainerInitializer_CleanupVolumeRemoveFailure(t *testing.T) {
	// When volume cleanup fails, the original error is still returned
	// and cleanup warnings include failure information.
	fake := dockertest.NewFakeClient()

	// Volumes freshly created — track state so IsVolumeManaged works during cleanup
	createdVols := map[string]bool{}
	fake.FakeAPI.VolumeInspectFn = func(_ context.Context, id string, _ moby.VolumeInspectOptions) (moby.VolumeInspectResult, error) {
		if createdVols[id] {
			return moby.VolumeInspectResult{
				Volume: volume.Volume{
					Name:   id,
					Labels: map[string]string{docker.LabelManaged: docker.ManagedLabelValue},
				},
			}, nil
		}
		return moby.VolumeInspectResult{}, testNotFoundError{msg: "No such volume: " + id}
	}
	fake.FakeAPI.VolumeCreateFn = func(_ context.Context, opts moby.VolumeCreateOptions) (moby.VolumeCreateResult, error) {
		createdVols[opts.Name] = true
		return moby.VolumeCreateResult{}, nil
	}

	// VolumeRemove fails during cleanup
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, _ string, _ moby.VolumeRemoveOptions) (moby.VolumeRemoveResult, error) {
		return moby.VolumeRemoveResult{}, fmt.Errorf("volume in use")
	}

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	// Config init will fail (triggering cleanup), and VolumeRemove will also fail
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-cleanup-test-dir")

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	// Original error is preserved (not overridden by cleanup failure)
	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")

	// Warnings should indicate cleanup failure
	warnings := ci.CleanupWarnings()
	require.NotEmpty(t, warnings, "should have cleanup failure warnings")
	foundFailure := false
	for _, w := range warnings {
		if strings.Contains(w, "Failed to clean up") {
			foundFailure = true
			break
		}
	}
	require.True(t, foundFailure, "warnings should mention cleanup failure: %v", warnings)
}

func TestContainerInitializer_InvalidAgentName(t *testing.T) {
	// Invalid agent name is rejected before any volumes are created.
	fake := dockertest.NewFakeClient()

	tio := iostreams.NewTestIOStreams()
	ci := testInitializer(tio.IOStreams)

	cmd := testFlags()
	containerOpts := copts.NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "--rm" // Invalid: starts with hyphen

	_, err := ci.Run(context.Background(), InitParams{
		Client:           fake.Client,
		Config:           testConfig(),
		ContainerOptions: containerOpts,
		Flags:            cmd.Flags(),
		Image:            "alpine",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent name")

	// Nothing should have been called — validation happens before Docker API calls
	fake.AssertNotCalled(t, "VolumeExists")
	fake.AssertNotCalled(t, "VolumeCreate")
	fake.AssertNotCalled(t, "ContainerCreate")
	require.Empty(t, ci.CleanupWarnings())
}
