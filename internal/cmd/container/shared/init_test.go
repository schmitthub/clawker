package shared

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/volume"
	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/logger/loggertest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testNotFoundError satisfies errdefs.IsNotFound for test fakes.
type testNotFoundError struct{ msg string }

func (e testNotFoundError) Error() string { return e.msg }
func (e testNotFoundError) NotFound()     {}

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
	containerOpts := NewContainerOptions()
	cmd := &cobra.Command{Use: "test"}
	AddFlags(cmd.Flags(), containerOpts)
	return cmd
}

// testCreateConfig builds a CreateContainerConfig with test defaults.
func testCreateConfig(fake *dockertest.FakeClient, cfg *config.Project, containerOpts *ContainerOptions, cmd *cobra.Command) *CreateContainerConfig {
	return &CreateContainerConfig{
		Client:  fake.Client,
		Config:  cfg,
		Options: containerOpts,
		Flags:   cmd.Flags(),
		Logger:  loggertest.NewNop(),
		GitManager: func() (*git.GitManager, error) {
			return nil, fmt.Errorf("GitManager not available in test")
		},
		HostProxy: func() hostproxy.HostProxyService {
			return hostproxytest.NewMockManager()
		},
	}
}

func TestCreateContainer_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
	require.Equal(t, "test-agent", result.AgentName)
	require.Equal(t, "clawker.testproject.test-agent", result.ContainerName)
	fake.AssertCalled(t, "ContainerCreate")
}

func TestCreateContainer_ContainerCreateError(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
	}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")
	require.Nil(t, result)
}

func TestCreateContainer_ConfigCached(t *testing.T) {
	// Default fake: volumes exist → ConfigCreated=false → config step is cached
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
}

func TestCreateContainer_ConfigFresh(t *testing.T) {
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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")
}

func TestCreateContainer_HostProxyFailure(t *testing.T) {
	// Host proxy enabled in config, but proxy manager fails — non-fatal, continues with warning
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	hostProxyEnabled := true
	cfg.Security.EnableHostProxy = &hostProxyEnabled

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	ccfg := &CreateContainerConfig{
		Client:  fake.Client,
		Config:  cfg,
		Options: containerOpts,
		Flags:   cmd.Flags(),
		Logger:  loggertest.NewNop(),
		GitManager: func() (*git.GitManager, error) {
			return nil, fmt.Errorf("GitManager not available in test")
		},
		HostProxy: func() hostproxy.HostProxyService {
			return hostproxytest.NewFailingMockManager(fmt.Errorf("mock host proxy failure"))
		},
	}

	// Collect events to check for warnings
	events := make(chan CreateContainerEvent, 64)
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for range events {
		}
	}()

	result, err := CreateContainer(context.Background(), ccfg, events)
	close(events)
	<-eventsDone

	// Should succeed despite host proxy failure (non-fatal)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.HostProxyRunning, "host proxy should not be running")
	require.NotEmpty(t, result.ContainerID)
}

func TestCreateContainer_OnboardingSkippedWhenDisabled(t *testing.T) {
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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	fake.AssertNotCalled(t, "CopyToContainer")
}

func TestCreateContainer_PostInit(t *testing.T) {
	// PostInit configured → CopyToContainer called for post-init script injection
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.PostInit = "npm install -g typescript\n"

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	// CopyToContainer should be called — for onboarding + post-init
	fake.AssertCalled(t, "CopyToContainer")
	// Verify it was called at least twice (onboarding + post-init)
	fake.AssertCalledN(t, "CopyToContainer", 2)
}

func TestCreateContainer_NoPostInit(t *testing.T) {
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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	fake.AssertNotCalled(t, "CopyToContainer")
}

func TestCreateContainer_PostInitInjectionError(t *testing.T) {
	// PostInit configured but CopyToContainer fails on the second call (post-init injection).
	// First call (onboarding) succeeds, second call (post-init) fails.
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupContainerRemove() // CreateContainer cleans up on injection failure

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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "inject post-init script")
	require.Nil(t, result)
}

func TestCreateContainer_EmptyProject(t *testing.T) {
	// Empty project → 2-segment container name (clawker.agent)
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Project = "" // empty project

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "myagent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "myagent", result.AgentName)
	require.Equal(t, "clawker.myagent", result.ContainerName)
}

func TestCreateContainer_EnvFileError(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.EnvFile = []string{"/nonexistent/file.env"}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "agent.env_file")
	fake.AssertNotCalled(t, "ContainerCreate")
}

func TestCreateContainer_FromEnvWarnings(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.FromEnv = []string{"CLAWKER_NONEXISTENT_VAR_99999"}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	// Collect events to check for warnings
	events := make(chan CreateContainerEvent, 64)
	var warnings []string
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for ev := range events {
			if ev.Type == MessageWarning {
				warnings = append(warnings, ev.Message)
			}
		}
	}()

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), events)
	close(events)
	<-eventsDone

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, warnings)
	foundEnvWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "CLAWKER_NONEXISTENT_VAR_99999") {
			foundEnvWarning = true
			break
		}
	}
	require.True(t, foundEnvWarning, "expected warning about CLAWKER_NONEXISTENT_VAR_99999, got: %v", warnings)
}

func TestCreateContainer_RandomAgentName(t *testing.T) {
	// No agent specified → random name generated
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	// No agent or name set

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.AgentName, "should have generated a random agent name")
}

func TestCreateContainer_CleanupVolumesOnCreateError(t *testing.T) {
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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	// Config init will fail because CLAUDE_CONFIG_DIR is invalid.
	// This happens AFTER volumes are created but BEFORE ContainerCreate,
	// which triggers the deferred cleanup of those newly-created volumes.
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-cleanup-test-dir")

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")

	// Verify volumes were cleaned up
	require.NotEmpty(t, removedVolumes, "should have cleaned up newly-created volumes")
}

func TestCreateContainer_NoCleanupForPreExistingVolumes(t *testing.T) {
	// When volumes already exist (ConfigCreated=false), no cleanup on failure.
	fake := dockertest.NewFakeClient()
	// Default: VolumeExists returns true → no volumes created

	// ContainerCreate fails
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		return moby.ContainerCreateResult{}, fmt.Errorf("image not found")
	}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")

	// No VolumeRemove calls — volumes were pre-existing
	fake.AssertNotCalled(t, "VolumeRemove")
}

func TestCreateContainer_CleanupVolumeRemoveFailure(t *testing.T) {
	// When volume cleanup fails, the original error is still returned.
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

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	// Config init will fail (triggering cleanup), and VolumeRemove will also fail
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/nonexistent-clawker-cleanup-test-dir")

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	// Original error is preserved (not overridden by cleanup failure)
	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")
}

func TestCreateContainer_InvalidAgentName(t *testing.T) {
	// Invalid agent name is rejected before any volumes are created.
	fake := dockertest.NewFakeClient()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "--rm" // Invalid: starts with hyphen

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent name")

	// Nothing should have been called — validation happens before Docker API calls
	fake.AssertNotCalled(t, "VolumeExists")
	fake.AssertNotCalled(t, "VolumeCreate")
	fake.AssertNotCalled(t, "ContainerCreate")
}

func TestCreateContainer_DisableFirewall(t *testing.T) {
	// When DisableFirewall=true, firewall env vars should NOT be set
	// even when the config has firewall enabled.
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"
	containerOpts.DisableFirewall = true

	cfg := testConfig()
	cfg.Security.Firewall = &config.FirewallConfig{
		Enable:          true,
		OverrideDomains: []string{"example.com"},
	}

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify no firewall env vars were injected
	for _, e := range containerOpts.Env {
		require.NotContains(t, e, "CLAWKER_FIREWALL_DOMAINS",
			"firewall env var should not be set when DisableFirewall=true")
		require.NotContains(t, e, "CLAWKER_FIREWALL_ENABLED",
			"CLAWKER_FIREWALL_ENABLED should not be set when DisableFirewall=true")
	}
	fake.AssertCalled(t, "ContainerCreate")
}

func TestCreateContainer_DisableFirewallFalse(t *testing.T) {
	// When DisableFirewall=false (default), firewall env vars should be set
	// when the config has firewall enabled.
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	cfg := testConfig()
	cfg.Security.Firewall = &config.FirewallConfig{
		Enable:          true,
		OverrideDomains: []string{"example.com"},
	}

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify firewall env vars were injected (firewall enabled, not disabled)
	var firewallDomainsFound, firewallEnabledFound bool
	for _, e := range containerOpts.Env {
		if strings.HasPrefix(e, "CLAWKER_FIREWALL_DOMAINS=") {
			firewallDomainsFound = true
		}
		if e == "CLAWKER_FIREWALL_ENABLED=true" {
			firewallEnabledFound = true
		}
	}
	require.True(t, firewallDomainsFound,
		"CLAWKER_FIREWALL_DOMAINS should be set when DisableFirewall=false and config has firewall enabled")
	require.True(t, firewallEnabledFound,
		"CLAWKER_FIREWALL_ENABLED=true should be set when DisableFirewall=false and config has firewall enabled")
}

func TestCreateContainer_EventsSequence(t *testing.T) {
	// Verify events are sent in expected order with expected steps.
	fake := dockertest.NewFakeClient()
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	events := make(chan CreateContainerEvent, 64)
	var collected []CreateContainerEvent
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for ev := range events {
			collected = append(collected, ev)
		}
	}()

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), events)
	close(events)
	<-eventsDone

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify we got events for all major steps
	steps := make(map[string]bool)
	for _, ev := range collected {
		steps[ev.Step] = true
	}
	require.True(t, steps["workspace"], "should have workspace events")
	require.True(t, steps["config"], "should have config events")
	require.True(t, steps["environment"], "should have environment events")
	require.True(t, steps["container"], "should have container events")

	// Verify first event is workspace running
	require.Equal(t, "workspace", collected[0].Step)
	require.Equal(t, StepRunning, collected[0].Status)

	// Verify last event is container complete
	last := collected[len(collected)-1]
	require.Equal(t, "container", last.Step)
	require.Equal(t, StepComplete, last.Status)
}
