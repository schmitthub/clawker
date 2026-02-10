package shared

import (
	"context"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/keyring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// InitContainerConfig tests
// ---------------------------------------------------------------------------

func TestInitContainerConfig_FreshStrategy_NoHostAuth(t *testing.T) {
	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: boolPtr(false),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 0, tracker.callCount(), "should not call CopyToVolume for fresh+no-auth")
}

func TestInitContainerConfig_FreshStrategy_WithHostAuth(t *testing.T) {
	keyring.MockInit()
	seedTestCredentials(t)

	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: boolPtr(true),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Should call CopyToVolume once for credentials
	require.Equal(t, 1, tracker.callCount(), "should call CopyToVolume once for credentials")

	call := tracker.calls()[0]
	assert.Equal(t, "clawker.myapp.ralph-config", call.volumeName)
	assert.Equal(t, "/home/claude/.claude", call.destPath)
}

func TestInitContainerConfig_CopyStrategy_NoHostAuth(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
			UseHostAuth: boolPtr(false),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Should call CopyToVolume once for config copy
	require.Equal(t, 1, tracker.callCount(), "should call CopyToVolume once for config")

	call := tracker.calls()[0]
	assert.Equal(t, "clawker.myapp.ralph-config", call.volumeName)
	assert.Equal(t, "/home/claude/.claude", call.destPath)

	// Verify the source directory contained staged content at call time
	assert.NotEmpty(t, call.srcDirEntries, "staged .claude dir should have had content at copy time")
}

func TestInitContainerConfig_CopyStrategy_WithHostAuth(t *testing.T) {
	keyring.MockInit()
	seedTestCredentials(t)

	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	seedHostConfigDir(t, hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
			UseHostAuth: boolPtr(true),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Should call CopyToVolume twice: once for config, once for credentials
	require.Equal(t, 2, tracker.callCount(), "should call CopyToVolume for config and credentials")

	// First call: config copy
	configCall := tracker.calls()[0]
	assert.Equal(t, "clawker.myapp.ralph-config", configCall.volumeName)

	// Second call: credentials copy
	credsCall := tracker.calls()[1]
	assert.Equal(t, "clawker.myapp.ralph-config", credsCall.volumeName)
}

func TestInitContainerConfig_NilClaudeCode_Defaults(t *testing.T) {
	// nil ClaudeCode should use defaults: copy strategy + host auth enabled
	keyring.MockInit()
	seedTestCredentials(t)

	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode:       nil, // defaults: copy strategy, use_host_auth true
		CopyToVolume:     tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Default is copy + use_host_auth=true, so should copy config + credentials
	require.Equal(t, 2, tracker.callCount(), "nil ClaudeCode should use defaults (copy + host auth)")
}

func TestInitContainerConfig_EmptyProject_VolumeNaming(t *testing.T) {
	keyring.MockInit()
	seedTestCredentials(t)

	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "", // empty project
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			UseHostAuth: boolPtr(true),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.NoError(t, err)

	// Default copy strategy + host auth = 2 calls (config + credentials)
	require.Equal(t, 2, tracker.callCount())
	// 2-segment: clawker.ralph-config (no project segment)
	assert.Equal(t, "clawker.ralph-config", tracker.calls()[0].volumeName)
	assert.Equal(t, "clawker.ralph-config", tracker.calls()[1].volumeName)
}

func TestInitContainerConfig_CopyToVolumeError(t *testing.T) {
	keyring.MockInit()
	seedTestCredentials(t)

	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			UseHostAuth: boolPtr(true),
		},
		CopyToVolume: func(_ context.Context, _, _, _ string, _ []string) error {
			return assert.AnError
		},
	}

	err := InitContainerConfig(context.Background(), opts)
	require.Error(t, err)
	// Default copy strategy means config copy fails first
	assert.Contains(t, err.Error(), "failed to copy claude config to volume")
}

func TestInitContainerConfig_HostConfigDirNotFound(t *testing.T) {
	// When strategy is "copy" but host config dir doesn't exist
	t.Setenv("CLAUDE_CONFIG_DIR", "/no/such/dir-init-test")
	t.Setenv("HOME", t.TempDir()) // no ~/.claude either

	tracker := &copyTracker{}
	opts := InitConfigOpts{
		ProjectName:      "myapp",
		AgentName:        "ralph",
		ContainerWorkDir: "/workspace",
		ClaudeCode: &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
			UseHostAuth: boolPtr(false),
		},
		CopyToVolume: tracker.copyToVolumeFn(),
	}

	err := InitContainerConfig(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot copy claude config")
}

// ---------------------------------------------------------------------------
// InjectOnboardingFile tests
// ---------------------------------------------------------------------------

func TestInjectOnboardingFile(t *testing.T) {
	tracker := &containerCopyTracker{}
	opts := InjectOnboardingOpts{
		ContainerID:     "abc123",
		CopyToContainer: tracker.copyFn(),
	}

	err := InjectOnboardingFile(context.Background(), opts)
	require.NoError(t, err)

	require.Equal(t, 1, tracker.callCount())
	call := tracker.calls()[0]
	assert.Equal(t, "abc123", call.containerID)
	assert.Equal(t, "/home/claude", call.destPath)
	assert.NotNil(t, call.content, "tar content should not be nil")
}

func TestInjectOnboardingFile_CopyError(t *testing.T) {
	opts := InjectOnboardingOpts{
		ContainerID: "abc123",
		CopyToContainer: func(_ context.Context, _, _ string, _ io.Reader) error {
			return assert.AnError
		},
	}

	err := InjectOnboardingFile(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to inject onboarding file")
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

// seedTestCredentials sets up mock keyring with valid claude code credentials.
func seedTestCredentials(t *testing.T) {
	t.Helper()

	u, err := user.Current()
	require.NoError(t, err)

	validJSON := `{
		"claudeAiOauth": {
			"accessToken":      "test-access",
			"refreshToken":     "test-refresh",
			"expiresAt":        4102444800000,
			"scopes":           ["scope1"],
			"subscriptionType": "pro",
			"rateLimitTier":    "tier1"
		},
		"organizationUuid": "550e8400-e29b-41d4-a716-446655440000"
	}`

	require.NoError(t, keyring.Set("Claude Code-credentials", u.Username, validJSON))
}

// seedHostConfigDir creates minimal test data in a host config dir.
func seedHostConfigDir(t *testing.T, dir string) {
	t.Helper()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "test-agent.md"), []byte("# Test Agent"), 0o644))
}
