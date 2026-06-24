package shared

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/volume"
	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/logger"
	projectpkg "github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testNotFoundError satisfies errdefs.IsNotFound for test fakes.
type testNotFoundError struct{ msg string }

func (e testNotFoundError) Error() string { return e.msg }
func (e testNotFoundError) NotFound()     {}

// testConfigYAML is the base YAML for init test configs.
// Loaded through NewFromString (real config pipeline, not direct struct construction).
// mount_projects is disabled so SetupMounts doesn't depend on host ~/.claude state
// (and so a deliberately-bad CLAUDE_CONFIG_DIR exercises InitContainerConfig, not the
// projects bind-mount resolver).
const testConfigYAML = `
workspace:
  default_mode: bind
security:
  enable_host_proxy: false
agent:
  claude_code:
    mount_projects: false
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

// testMockConfig returns a *configmocks.ConfigMock for container-create tests.
// Project-root / ignore-file resolution goes through the injected
// ProjectRegistry closure, which reads the registry from the isolated data
// dir set up by setupAuthEnv (testenv.New): with no registry on disk,
// CreateContainer degrades to empty ignore patterns — the intended "not in
// a project" behavior.
// The cfg parameter ensures Project() returns a consistent project name for volume naming.
func testMockConfig(project *config.Project) *configmocks.ConfigMock {
	mock := configmocks.NewBlankConfig()
	if project != nil {
		mock.ProjectFunc = func() *config.Project { return project }
	}
	return mock
}

// testCreateConfig builds a CreateContainerOptions with test defaults.
func testCreateConfig(fake *mocks.FakeClient, project *config.Project, containerOpts *ContainerCreateOptions, cmd *cobra.Command) *CreateContainerOptions {
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
		ProjectRegistry: func() (*projectpkg.Registry, error) {
			return projectpkg.NewRegistry()
		},
		HostProxy: func() hostproxy.Service {
			return hostproxytest.NewMockManager()
		},
	}
}

func TestCreateContainer_HappyPath(t *testing.T) {
	setupAuthEnv(t)
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
	require.Equal(t, "test-agent", result.AgentName)
	require.Equal(t, "clawker.testproject.test-agent", result.ContainerName)
	fake.AssertCalled(t, "ContainerCreate")
}

// snapshotModeProject builds a *config.Project whose workspace.default_mode is
// snapshot, for exercising the config-default branch of the worktree guard.
func snapshotModeProject(t *testing.T) *config.Project {
	t.Helper()
	cfg, err := config.NewFromString("workspace:\n  default_mode: snapshot\n", "")
	require.NoError(t, err)
	return cfg.Project()
}

// TestCreateContainer_RejectsWorktreeInSnapshotMode locks in the fail-fast
// guard: worktree + snapshot must be rejected before resolveWorkDir creates a
// git worktree on disk. This is a distinct guard from the one in
// workspace.SetupMounts. ContainerCreate must never be called — the rejection
// happens before any Docker work. Covers both the --mode override and the
// config workspace.default_mode paths.
func TestCreateContainer_RejectsWorktreeInSnapshotMode(t *testing.T) {
	setupAuthEnv(t)
	tests := []struct {
		name    string
		project *config.Project
		mode    string
	}{
		{name: "explicit --mode snapshot override", project: testConfig(), mode: "snapshot"},
		{name: "config workspace.default_mode: snapshot", project: snapshotModeProject(t), mode: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := mocks.NewFakeClient(configmocks.NewBlankConfig())

			containerOpts := NewContainerOptions()
			containerOpts.Image = "alpine"
			containerOpts.Agent = "test-agent"
			containerOpts.Worktree = "feature/x"
			containerOpts.Mode = tt.mode

			_, err := CreateContainer(context.Background(),
				testCreateConfig(fake, tt.project, containerOpts, testFlags()))

			require.ErrorIs(t, err, workspace.ErrWorktreeSnapshot)
			fake.AssertNotCalled(t, "ContainerCreate")
		})
	}
}

func TestCreateContainer_ContainerCreateError(t *testing.T) {
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		return moby.ContainerCreateResult{}, fmt.Errorf("disk full")
	}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")
	require.Nil(t, result)
}

func TestCreateContainer_ConfigCached(t *testing.T) {
	setupAuthEnv(t)
	// Default fake: volumes exist → ConfigCreated=false → config step is cached
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
}

func TestCreateContainer_ConfigFresh(t *testing.T) {
	// Volumes don't exist → EnsureVolume creates → ConfigCreated=true → init runs
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
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
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")
}

func TestCreateContainer_PostInit(t *testing.T) {
	// PostInit configured → CopyToContainer called for post-init script injection
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.PostInit = "npm install -g typescript\n"

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	// CopyToContainer is called twice: once for the bootstrap material
	// (InstallAgentBootstrap, always) and once for the post-init script.
	fake.AssertCalledN(t, "CopyToContainer", 2)
}

func TestCreateContainer_NoPostInit(t *testing.T) {
	// No PostInit configured → no CopyToContainer calls
	cfg := testConfig()
	useHostAuth := false
	cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
		UseHostAuth: &useHostAuth,
		Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
	}

	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	// CopyToContainer is now ALWAYS called by CreateContainer because
	// InstallAgentBootstrap tars per-agent mTLS material into the
	// container's writable layer regardless of post-init configuration.
	// We still verify post-init didn't fire by asserting the
	// bootstrap-material copy is the only one (single CopyToContainer).
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
	// Exactly one CopyToContainer call — bootstrap material. Post-init
	// would have produced a second call.
	fake.AssertCalledN(t, "CopyToContainer", 1)
}

func TestCreateContainer_PostInitInjectionError(t *testing.T) {
	// PostInit configured. The bootstrap material copy must succeed
	// (so we reach the post-init step) and only the SECOND
	// CopyToContainer call (post-init script) fails. Counts and fails
	// the second invocation.
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupContainerRemove() // CreateContainer cleans up on injection failure

	var copyCalls int
	fake.FakeAPI.CopyToContainerFn = func(_ context.Context, _ string, _ moby.CopyToContainerOptions) (moby.CopyToContainerResult, error) {
		copyCalls++
		if copyCalls == 1 {
			// Bootstrap material — let it succeed.
			return moby.CopyToContainerResult{}, nil
		}
		// Post-init script — fail.
		return moby.CopyToContainerResult{}, fmt.Errorf("simulated copy failure")
	}

	cfg := testConfig()
	cfg.Agent.PostInit = "npm install -g typescript\n"

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "inject post-init script")
	require.Nil(t, result)
}

func TestCreateContainer_EmptyProject(t *testing.T) {
	// Empty project → 2-segment container name (clawker.agent)
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
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

	result, err := CreateContainer(context.Background(), cc)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "myagent", result.AgentName)
	require.Equal(t, "clawker.myagent", result.ContainerName)
}

func TestCreateContainer_EnvFileError(t *testing.T) {
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cfg := testConfig()
	cfg.Agent.EnvFile = []string{"/nonexistent/file.env"}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "agent.env_file")
	fake.AssertNotCalled(t, "ContainerCreate")
}

func TestCreateContainer_RandomAgentName(t *testing.T) {
	// No agent specified → random name generated
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	// No agent or name set

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.AgentName, "should have generated a random agent name")
}

func TestCreateContainer_CleanupVolumesOnCreateError(t *testing.T) {
	// When volumes are freshly created and a subsequent init step fails,
	// deferred cleanup removes newly-created volumes.
	cfg := configmocks.NewBlankConfig()
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(cfg)

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
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")

	// Verify volumes were cleaned up
	require.NotEmpty(t, removedVolumes, "should have cleaned up newly-created volumes")
}

func TestCreateContainer_NoCleanupForPreExistingVolumes(t *testing.T) {
	// When volumes already exist (ConfigCreated=false), no cleanup on failure.
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
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
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "creating container")

	// No VolumeRemove calls — volumes were pre-existing
	fake.AssertNotCalled(t, "VolumeRemove")
}

func TestCreateContainer_CleanupVolumeRemoveFailure(t *testing.T) {
	// When volume cleanup fails, the original error is still returned.
	cfg := configmocks.NewBlankConfig()
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(cfg)

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
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	// Original error is preserved (not overridden by cleanup failure)
	require.Error(t, err)
	require.Contains(t, err.Error(), "container init")
}

func TestCreateContainer_InvalidAgentName(t *testing.T) {
	// Invalid agent name is rejected before any volumes are created.
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "--rm" // Invalid: starts with hyphen

	_, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent name")

	// Nothing should have been called — validation happens before Docker API calls
	fake.AssertNotCalled(t, "VolumeExists")
	fake.AssertNotCalled(t, "VolumeCreate")
	fake.AssertNotCalled(t, "ContainerCreate")
}

func TestCreateContainer_FirewallEnabledFromSettings(t *testing.T) {
	// FirewallEnabled env var is set from settings alone — no FirewallManager needed.
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerCreate()
	fake.SetupCopyToContainer()

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"

	cfg := testConfigWithFirewall("example.com")

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, cfg, containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)
	fake.AssertCalled(t, "ContainerCreate")
}

func TestCreateContainer_WorkingDirDefault(t *testing.T) {
	// When --workdir is NOT set, WorkingDir defaults to wsResult.ContainerPath
	// (the host absolute path used for session persistence).
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupCopyToContainer()

	// Capture the ContainerCreateOptions to inspect WorkingDir
	var capturedWorkingDir string
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, opts moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		if opts.Config != nil {
			capturedWorkingDir = opts.Config.WorkingDir
		}
		return moby.ContainerCreateResult{ID: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"}, nil
	}

	cmd := testFlags()
	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"
	// Workdir intentionally left empty — default behavior

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)

	// WorkingDir should be set to a non-empty absolute path (the host cwd)
	require.NotEmpty(t, capturedWorkingDir, "WorkingDir should default to wsResult.ContainerPath")
	require.True(t, filepath.IsAbs(capturedWorkingDir),
		"default WorkingDir should be an absolute path, got %q", capturedWorkingDir)
}

func TestCreateContainer_WorkingDirOverride(t *testing.T) {
	// When --workdir is explicitly set, it should override the default.
	setupAuthEnv(t)
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupCopyToContainer()

	var capturedWorkingDir string
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, opts moby.ContainerCreateOptions) (moby.ContainerCreateResult, error) {
		if opts.Config != nil {
			capturedWorkingDir = opts.Config.WorkingDir
		}
		return moby.ContainerCreateResult{ID: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"}, nil
	}

	cmd := testFlags()
	// Simulate --workdir flag being set
	require.NoError(t, cmd.Flags().Set("workdir", "/custom/work/dir"))

	containerOpts := NewContainerOptions()
	containerOpts.Image = "alpine"
	containerOpts.Agent = "test-agent"
	containerOpts.Workdir = "/custom/work/dir"

	result, err := CreateContainer(context.Background(),
		testCreateConfig(fake, testConfig(), containerOpts, cmd))

	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, "/custom/work/dir", capturedWorkingDir,
		"WorkingDir should match the explicit --workdir value")
}
