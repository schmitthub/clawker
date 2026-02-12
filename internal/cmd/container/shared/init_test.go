package shared

import (
	"context"
	"fmt"
	"testing"

	moby "github.com/moby/moby/client"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

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
