package shared

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// testClaudeStaging mirrors the claude bundle's staging manifest — the
// shape InitContainerConfig consumed implicitly before staging went
// manifest-driven.
func testClaudeStaging() config.Staging {
	return config.Staging{
		Copy: []config.CopySpec{
			{
				Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json", Dest: ".claude/settings.json",
				JSONKeys: []string{"enabledPlugins"}, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/CLAUDE.md", Dest: ".claude/CLAUDE.md",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/agents", Dest: ".claude/agents",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/skills", Dest: ".claude/skills",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: "${CLAUDE_CONFIG_DIR:-~/.claude}/commands", Dest: ".claude/commands",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
		},
		Mounts: nil,
	}
}

// testHarnessCfg builds a fully-specified HarnessConfig for init tests; the
// per-harness env/hook fields are irrelevant to config-volume init.
func testHarnessCfg(strategy string) *config.HarnessConfig {
	return &config.HarnessConfig{
		Config:        config.HarnessConfigOptions{Strategy: strategy},
		MountProjects: nil,
		EnvFile:       nil,
		FromEnv:       nil,
		Env:           nil,
		PostInit:      "",
		PreRun:        "",
	}
}

// ---------------------------------------------------------------------------
// InitContainerConfig tests
// ---------------------------------------------------------------------------

func TestInitContainerConfig_FreshStrategy_NoCopy(t *testing.T) {
	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          testHarnessCfg("fresh"),
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume:     tracker.copyToVolumeFn(),
		Log:              logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 0, tracker.callCount(), "fresh strategy must not copy anything to the volume")
}

func TestInitContainerConfig_CopyStrategy(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          testHarnessCfg("copy"),
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume:     tracker.copyToVolumeFn(),
		Log:              logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Should call CopyToVolume once for config copy — never for credentials.
	require.Equal(t, 1, tracker.callCount(), "should call CopyToVolume once for config")

	call := tracker.calls()[0]
	assert.Equal(t, "clawker.myapp.dev-claude.config", call.volumeName)
	assert.Equal(t, "/home/clawker/.claude", call.destPath)

	// Verify the source directory contained staged content at call time
	assert.NotEmpty(t, call.srcDirEntries, "staged .claude dir should have had content at copy time")
}

func TestInitContainerConfig_NilHarnessCfg_Defaults(t *testing.T) {
	// nil harness config should use defaults: copy strategy.
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          nil, // defaults: copy strategy
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume:     tracker.copyToVolumeFn(),
		Log:              logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.callCount(), "nil harness config should default to copy strategy")
}

func TestInitContainerConfig_EmptyProject_VolumeNaming(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "", // empty project
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          testHarnessCfg(""),
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume:     tracker.copyToVolumeFn(),
		Log:              logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	require.Equal(t, 1, tracker.callCount())
	// 2-segment: clawker.dev-claude.config (no project segment)
	assert.Equal(t, "clawker.dev-claude.config", tracker.calls()[0].volumeName)
}

func TestInitContainerConfig_CopyToVolumeError(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          testHarnessCfg(""),
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume: func(_ context.Context, _, _, _ string, _ []string) error {
			return assert.AnError
		},
		Log: logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to copy harness config to volume")
}

func TestInitContainerConfig_HostConfigDirNotFound(t *testing.T) {
	// Missing host state is a soft skip: nothing stages, nothing copies —
	// same semantics as any other missing copy source.
	t.Setenv("CLAUDE_CONFIG_DIR", "/no/such/dir-init-test")
	t.Setenv("HOME", t.TempDir()) // no ~/.claude either

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "dev",
		HarnessName:      "claude",
		ContainerWorkDir: "/workspace",
		Harness:          testHarnessCfg("copy"),
		Staging:          testClaudeStaging(),
		Volumes:          []config.VolumeSpec{{Name: "config", Path: ".claude"}},
		FreshVolumes:     map[string]bool{"config": true},
		CopyToVolume:     tracker.copyToVolumeFn(),
		Log:              logger.Nop(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 0, tracker.callCount(), "nothing staged → nothing copied")
}

// ---------------------------------------------------------------------------
// InjectPostInitScript tests
// ---------------------------------------------------------------------------

func TestInjectPostInitScript(t *testing.T) {
	tracker := &containerCopyTracker{}
	opts := InjectPostInitOpts{
		ContainerID:     "abc123",
		Script:          "npm install -g typescript\n",
		Cfg:             configmocks.NewBlankConfig(),
		CopyToContainer: tracker.copyFn(),
		Log:             logger.Nop(),
	}

	err := InjectPostInitScript(context.Background(), opts)
	require.NoError(t, err)

	require.Equal(t, 1, tracker.callCount())
	call := tracker.calls()[0]
	assert.Equal(t, "abc123", call.containerID)
	assert.Equal(t, "/home/clawker", call.destPath)
	assert.NotNil(t, call.content, "tar content should not be nil")
}

func TestInjectPostInitScript_CopyError(t *testing.T) {
	opts := InjectPostInitOpts{
		ContainerID: "abc123",
		Script:      "echo hello\n",
		Cfg:         configmocks.NewBlankConfig(),
		CopyToContainer: func(_ context.Context, _, _ string, _ io.Reader) error {
			return assert.AnError
		},
		Log: logger.Nop(),
	}

	err := InjectPostInitScript(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to inject post-init script")
}

func TestInjectPostInitScript_NilCopyFn(t *testing.T) {
	opts := InjectPostInitOpts{
		ContainerID: "abc123",
		Script:      "echo hello\n",
		Log:         logger.Nop(),
	}

	err := InjectPostInitScript(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CopyToContainerFn is required")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type copyToVolumeCall struct {
	volumeName    string
	srcDir        string
	destPath      string
	srcDirEntries []string // captured at call time before cleanup
}

type copyTracker struct {
	mu       sync.Mutex
	recorded []copyToVolumeCall
}

func (ct *copyTracker) copyToVolumeFn() func(ctx context.Context, volumeName, srcDir, destPath string, ignorePatterns []string) error {
	return func(_ context.Context, volumeName, srcDir, destPath string, _ []string) error {
		ct.mu.Lock()
		defer ct.mu.Unlock()

		// Capture directory entries at call time (before staging dir cleanup)
		var entries []string
		if dirEntries, err := os.ReadDir(srcDir); err == nil {
			for _, e := range dirEntries {
				entries = append(entries, e.Name())
			}
		}

		ct.recorded = append(ct.recorded, copyToVolumeCall{
			volumeName:    volumeName,
			srcDir:        srcDir,
			destPath:      destPath,
			srcDirEntries: entries,
		})
		return nil
	}
}

func (ct *copyTracker) callCount() int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.recorded)
}

func (ct *copyTracker) calls() []copyToVolumeCall {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	result := make([]copyToVolumeCall, len(ct.recorded))
	copy(result, ct.recorded)
	return result
}

type containerCopyCall struct {
	containerID string
	destPath    string
	content     io.Reader
}

type containerCopyTracker struct {
	mu       sync.Mutex
	recorded []containerCopyCall
}

func (ct *containerCopyTracker) copyFn() func(ctx context.Context, containerID, destPath string, content io.Reader) error {
	return func(_ context.Context, containerID, destPath string, content io.Reader) error {
		ct.mu.Lock()
		defer ct.mu.Unlock()
		ct.recorded = append(ct.recorded, containerCopyCall{
			containerID: containerID,
			destPath:    destPath,
			content:     content,
		})
		return nil
	}
}

func (ct *containerCopyTracker) callCount() int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.recorded)
}

func (ct *containerCopyTracker) calls() []containerCopyCall {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	result := make([]containerCopyCall, len(ct.recorded))
	copy(result, ct.recorded)
	return result
}

// seedHostConfigDir creates minimal test data in a host config dir.
func seedHostConfigDir(t *testing.T, dir string) {
	t.Helper()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "test-agent.md"), []byte("# Test Agent"), 0o644))
}
