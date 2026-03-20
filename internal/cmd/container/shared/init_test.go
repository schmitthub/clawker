package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/volume"
	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/logger"
	projectpkg "github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testNotFoundError satisfies errdefs.IsNotFound for test fakes.
type testNotFoundError struct{ msg string }

func (e testNotFoundError) Error() string { return e.msg }
func (e testNotFoundError) NotFound()     {}

// testConfigYAML is the base YAML for init test configs.
// Loaded through NewFromString (real config pipeline, not direct struct construction).
const testConfigYAML = `
workspace:
  default_mode: bind
security:
  enable_host_proxy: false
  firewall:
    enable: false
`

// testConfig returns a minimal *config.Project loaded through NewFromString.
func testConfig() *config.Project {
	cfg, err := config.NewFromString(testConfigYAML, "")
	if err != nil {
		panic(fmt.Sprintf("testConfig: %v", err))
	}
	return cfg.Project()
}

// testConfigWithFirewall returns a *config.Project with firewall enabled and custom domains.
func testConfigWithFirewall(domains ...string) *config.Project {
	var b strings.Builder
	for _, d := range domains {
		b.WriteString("\n    - ")
		b.WriteString(d)
	}
	yaml := fmt.Sprintf(`
workspace:
  default_mode: bind
security:
  enable_host_proxy: false
  firewall:
    enable: true
    add_domains:%s
`, b.String())
	cfg, err := config.NewFromString(yaml, "")
	if err != nil {
		panic(fmt.Sprintf("testConfigWithFirewall: %v", err))
	}
	return cfg.Project()
}

// testFlags returns a FlagSet from a minimal cobra command with container flags registered.
func testFlags() *cobra.Command {
	containerOpts := NewContainerOptions()
	cmd := &cobra.Command{Use: "test"}
	AddFlags(cmd.Flags(), containerOpts)
	return cmd
}

// testMockConfig returns a *configmocks.ConfigMock with GetProjectIgnoreFile and
// GetProjectRoot stubbed to return safe temp paths (no registered project needed).
// The cfg parameter ensures Project() returns a consistent project name for volume naming.
func testMockConfig(project *config.Project) *configmocks.ConfigMock {
	mock := configmocks.NewBlankConfig()
	mock.GetProjectIgnoreFileFunc = func() (string, error) {
		return filepath.Join(os.TempDir(), mock.ClawkerIgnoreName()), nil
	}
	mock.GetProjectRootFunc = func() (string, error) {
		return os.TempDir(), nil
	}
	if project != nil {
		mock.ProjectFunc = func() *config.Project { return project }
	}
	return mock
}

// testCreateConfig builds a CreateContainerOptions with test defaults.
func testCreateConfig(fake *dockertest.FakeClient, project *config.Project, containerOpts *ContainerCreateOptions, cmd *cobra.Command) *CreateContainerOptions {
	return &CreateContainerOptions{
		Client:      fake.Client,
		Config:      testMockConfig(project),
		ProjectName: "testproject",
		Options:     containerOpts,
		Flags:       cmd.Flags(),
		Log:         logger.Nop(),
		ProjectManager: func() (projectpkg.ProjectManager, error) {
			return nil, fmt.Errorf("ProjectManager not available in test")
		},
		HostProxy: func() hostproxy.HostProxyService {
			return hostproxytest.NewMockManager()
		},
	}
}

func TestCreateContainer_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	projectCfg := testConfig()
	hostProxyEnabled := true
	projectCfg.Security.EnableHostProxy = &hostProxyEnabled

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	ccfg := &CreateContainerOptions{
		Client:  fake.Client,
		Config:  testMockConfig(projectCfg),
		Options: containerOpts,
		Flags:   cmd.Flags(),
		Log:     logger.Nop(),
		ProjectManager: func() (projectpkg.ProjectManager, error) {
			return nil, fmt.Errorf("ProjectManager not available in test")
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

func TestCreateContainer_PostInit(t *testing.T) {
	// PostInit configured → CopyToContainer called for post-init script injection
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	// CopyToContainer should be called for post-init script injection
	fake.AssertCalled(t, "CopyToContainer")
	fake.AssertCalledN(t, "CopyToContainer", 1)
}

func TestCreateContainer_NoPostInit(t *testing.T) {
	// No PostInit configured → no CopyToContainer calls
	cfg := testConfig()
	useHostAuth := false
	cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
		UseHostAuth: &useHostAuth,
		Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
	}

	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	// PostInit configured but CopyToContainer fails → post-init injection error propagates.
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupContainerRemove() // CreateContainer cleans up on injection failure

	// CopyToContainer fails → post-init injection error propagates
	fake.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ moby.CopyToContainerOptions) (moby.CopyToContainerResult, error) {
		return moby.CopyToContainerResult{}, fmt.Errorf("simulated copy failure")
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	// ProjectName defaults to "" on CreateContainerConfig (empty project)

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "myagent"

	cc := testCreateConfig(fake, cfg, containerOpts, cmd)
	cc.ProjectName = "" // empty project → 2-segment name

	result, err := CreateContainer(context.Background(), cc, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "myagent", result.AgentName)
	require.Equal(t, "clawker.myagent", result.ContainerName)
}

func TestCreateContainer_EnvFileError(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	cfg := configmocks.NewBlankConfig()
	fake := dockertest.NewFakeClient(cfg)

	// Track which volumes have been "created" — allows VolumeInspect to return
	// "not found" initially (so EnsureVolume creates) then "managed" (so VolumeRemove works).
	createdVols := map[string]bool{}
	fake.FakeAPI.VolumeInspectFn = func(_ context.Context, id string, _ moby.VolumeInspectOptions) (moby.VolumeInspectResult, error) {
		if createdVols[id] {
			return moby.VolumeInspectResult{
				Volume: volume.Volume{
					Name:   id,
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
	cfg := configmocks.NewBlankConfig()
	fake := dockertest.NewFakeClient(cfg)

	// Volumes freshly created — track state so IsVolumeManaged works during cleanup
	createdVols := map[string]bool{}
	fake.FakeAPI.VolumeInspectFn = func(_ context.Context, id string, _ moby.VolumeInspectOptions) (moby.VolumeInspectResult, error) {
		if createdVols[id] {
			return moby.VolumeInspectResult{
				Volume: volume.Volume{
					Name:   id,
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())

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

func TestCreateContainer_NoFirewallEnvWithoutManager(t *testing.T) {
	// Without a FirewallManager wired, no firewall env vars should be set
	// regardless of project config.
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	cfg := testConfigWithFirewall("example.com")

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify no firewall env vars were injected (no FirewallManager wired).
	for _, e := range containerOpts.Env {
		require.NotContains(t, e, "CLAWKER_FIREWALL",
			"firewall env vars should not be set without a FirewallManager")
	}
	fake.AssertCalled(t, "ContainerCreate")
}

func TestCreateContainer_WorkingDirDefault(t *testing.T) {
	// When --workdir is NOT set, WorkingDir defaults to wsResult.ContainerPath
	// (the host absolute path used for session persistence).
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupCopyToContainer()

	// Capture the ContainerCreateOptions to inspect WorkingDir
	var capturedWorkingDir string
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, opts moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		if opts.Config != nil {
			capturedWorkingDir = opts.Config.WorkingDir
		}
		return moby.ContainerCreateResult{ID: "sha256:fakecontainer1234567890abcdef"}, nil
	}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"
	// Workdir intentionally left empty — default behavior

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)

	// WorkingDir should be set to a non-empty absolute path (the host cwd)
	require.NotEmpty(t, capturedWorkingDir, "WorkingDir should default to wsResult.ContainerPath")
	require.True(t, filepath.IsAbs(capturedWorkingDir),
		"default WorkingDir should be an absolute path, got %q", capturedWorkingDir)
}

func TestCreateContainer_WorkingDirOverride(t *testing.T) {
	// When --workdir is explicitly set, it should override the default.
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupCopyToContainer()

	var capturedWorkingDir string
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, opts moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		if opts.Config != nil {
			capturedWorkingDir = opts.Config.WorkingDir
		}
		return moby.ContainerCreateResult{ID: "sha256:fakecontainer1234567890abcdef"}, nil
	}

	cmd := testFlags()
	// Simulate --workdir flag being set
	require.NoError(t, cmd.Flags().Set("workdir", "/custom/work/dir"))

	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"
	containerOpts.Workdir = "/custom/work/dir"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd), nil)

	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, "/custom/work/dir", capturedWorkingDir,
		"WorkingDir should match the explicit --workdir value")
}

func TestCreateContainer_EventsSequence(t *testing.T) {
	// Verify events are sent in expected order with expected steps.
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
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
