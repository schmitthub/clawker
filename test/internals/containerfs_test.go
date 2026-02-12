package internals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/containerfs"
	"github.com/schmitthub/clawker/internal/docker"
	whail "github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// containerHomeDir is the standard home directory for the claude user inside test containers.
const containerHomeDir = "/home/claude"

// ---------------------------------------------------------------------------
// Host fixture helpers (seed simulated host state that production code reads)
// ---------------------------------------------------------------------------

// seedHostConfigDir creates a realistic host config directory for testing.
func seedHostConfigDir(t *testing.T, hostDir string) {
	t.Helper()

	// settings.json with enabledPlugins
	settings := map[string]any{
		"enabledPlugins": map[string]any{
			"plugin-a": true,
			"plugin-b": false,
		},
		"someOtherKey": "should-not-be-copied",
	}
	writeJSONFile(t, filepath.Join(hostDir, "settings.json"), settings)

	// agents/ directory
	agentsDir := filepath.Join(hostDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.md"), []byte("# Test Agent"), 0o644))

	// skills/ directory
	skillsDir := filepath.Join(hostDir, "skills")
	require.NoError(t, os.MkdirAll(skillsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "skill.md"), []byte("# Test Skill"), 0o644))

	// commands/ directory
	commandsDir := filepath.Join(hostDir, "commands")
	require.NoError(t, os.MkdirAll(commandsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(commandsDir, "cmd.md"), []byte("# Test Command"), 0o644))

	// plugins/ directory with cache artifacts that should be excluded
	pluginsDir := filepath.Join(hostDir, "plugins")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "some-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "some-plugin", "plugin.js"), []byte("plugin-code"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "cache"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "cache", "cached.dat"), []byte("cached"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "install-counts-cache.json"), []byte("{}"), 0o644))

	// known_marketplaces.json with host-specific install paths
	marketplaces := []map[string]any{
		{
			"name":        "some-plugin",
			"installPath": filepath.Join(hostDir, "plugins", "some-plugin"),
		},
	}
	writeJSONFile(t, filepath.Join(pluginsDir, "known_marketplaces.json"), marketplaces)
}

// writeJSONFile is a helper that marshals v to JSON and writes it to path.
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err, "marshal JSON for %s", path)
	require.NoError(t, os.WriteFile(path, data, 0o644), "write %s", path)
}

// seedCredentialsFile creates a .credentials.json file in the host config dir.
func seedCredentialsFile(t *testing.T, hostDir string) {
	t.Helper()
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "test-access-token",
			"refreshToken":     "test-refresh-token",
			"expiresAt":        4102444800000,
			"scopes":           []string{"user:read"},
			"subscriptionType": "pro",
			"rateLimitTier":    "tier1",
		},
		"organizationUuid": "550e8400-e29b-41d4-a716-446655440000",
	}
	writeJSONFile(t, filepath.Join(hostDir, ".credentials.json"), creds)
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool { return &b }


// createConfigVolume creates a config volume, registers cleanup, and returns
// the volume name. This is the standard setup for tests that need to populate
// a volume via InitContainerConfig before starting a container.
func createConfigVolume(t *testing.T, ctx context.Context, client *docker.Client, project, agent string) string {
	t.Helper()
	volumeName := docker.VolumeName(project, agent, "config")
	_, err := client.EnsureVolume(ctx, volumeName, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, err := client.VolumeRemove(context.Background(), volumeName, true); err != nil {
			t.Logf("WARNING: failed to remove volume %s: %v", volumeName, err)
		}
	})
	return volumeName
}

// ---------------------------------------------------------------------------
// Rule: Copy strategy replicates select Claude Code config into the container
// ---------------------------------------------------------------------------

func TestContainerFs_CopyStrategy_ConfigInContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Seed host fixtures
	hostDir := t.TempDir()
	seedHostConfigDir(t, hostDir)
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	// Create config volume via production code path
	project, agent := "test", harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, project, agent)

	// Init config via production InitContainerConfig (copy strategy, no auth)
	claudeCode := &config.ClaudeCodeConfig{
		Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
		UseHostAuth: boolPtr(false),
	}
	err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
		ProjectName:      project,
		AgentName:        agent,
		ContainerWorkDir: "/workspace",
		ClaudeCode:       claudeCode,
		CopyToVolume:     client.CopyToVolume,
	})
	require.NoError(t, err, "InitContainerConfig failed")

	// Run container with config volume mounted
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
	)

	// --- Scenario: settings.json enabledPlugins are merged into container settings ---
	t.Run("settings_enabledPlugins_merged", func(t *testing.T) {
		settingsPath := containerHomeDir + "/.claude/settings.json"
		assert.True(t, ctr.FileExists(ctx, client, settingsPath),
			"settings.json should exist in the container")

		content, err := ctr.ReadFile(ctx, client, settingsPath)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &parsed), "unmarshal settings.json in container")

		_, hasPlugins := parsed["enabledPlugins"]
		assert.True(t, hasPlugins, "settings.json should contain enabledPlugins")

		_, hasOther := parsed["someOtherKey"]
		assert.False(t, hasOther, "settings.json should NOT contain non-enabledPlugins keys")
	})

	// --- Scenario Outline: Directories are copied in full to the container ---
	for _, dir := range []string{"agents", "skills", "commands"} {
		t.Run(fmt.Sprintf("directory_%s_copied", dir), func(t *testing.T) {
			dirPath := containerHomeDir + "/.claude/" + dir + "/"
			assert.True(t, ctr.DirExists(ctx, client, dirPath),
				"%s/ should exist in the container", dir)

			// Verify a known file is present
			var knownFile string
			switch dir {
			case "agents":
				knownFile = "agent.md"
			case "skills":
				knownFile = "skill.md"
			case "commands":
				knownFile = "cmd.md"
			}
			filePath := containerHomeDir + "/.claude/" + dir + "/" + knownFile
			assert.True(t, ctr.FileExists(ctx, client, filePath),
				"%s/%s should exist in the container", dir, knownFile)
		})
	}

	// --- Scenario: Plugins directory is copied including cache ---
	t.Run("plugins_cache_included", func(t *testing.T) {
		pluginsPath := containerHomeDir + "/.claude/plugins/"
		assert.True(t, ctr.DirExists(ctx, client, pluginsPath),
			"plugins/ should exist in the container")

		// some-plugin/ should be present
		assert.True(t, ctr.DirExists(ctx, client, pluginsPath+"some-plugin/"),
			"some-plugin/ should be present in plugins")

		// cache/ SHOULD be present (needed for plugin assets)
		assert.True(t, ctr.DirExists(ctx, client, pluginsPath+"cache/"),
			"cache/ should be present in plugins")
		assert.True(t, ctr.FileExists(ctx, client, pluginsPath+"cache/cached.dat"),
			"cache/cached.dat should be present in plugins")

		// install-counts-cache.json should NOT be present
		assert.False(t, ctr.FileExists(ctx, client, pluginsPath+"install-counts-cache.json"),
			"install-counts-cache.json should NOT be present in plugins")
	})

	// --- Scenario: known_marketplaces.json installPaths are rewritten for the container ---
	t.Run("known_marketplaces_rewritten", func(t *testing.T) {
		mpPath := containerHomeDir + "/.claude/plugins/known_marketplaces.json"
		assert.True(t, ctr.FileExists(ctx, client, mpPath),
			"known_marketplaces.json should exist in the container")

		content, err := ctr.ReadFile(ctx, client, mpPath)
		require.NoError(t, err)
		var entries []map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &entries), "unmarshal known_marketplaces.json")

		require.NotEmpty(t, entries, "known_marketplaces.json should have entries")
		for _, entry := range entries {
			installPath, ok := entry["installPath"].(string)
			if !ok {
				continue
			}
			// Should reference the container home, not the host temp dir
			assert.True(t, strings.HasPrefix(installPath, containerHomeDir+"/.claude/plugins/"),
				"installPath should reference container home, got: %s", installPath)
			assert.False(t, strings.Contains(installPath, hostDir),
				"installPath should NOT contain host path, got: %s", installPath)
		}
	})

	// --- Scenario: Files are readable by container user (UID 1001) ---
	t.Run("files_readable_by_claude_user", func(t *testing.T) {
		// Read settings.json as the claude user (default container user)
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/.claude/settings.json")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode,
			"settings.json should be readable by claude user; stderr=%s", result.Stderr)
	})
}

// TestContainerFs_CopyStrategy_MissingFilesSkipped verifies that initialization succeeds
// when the host config directory has none of the optional items.
func TestContainerFs_CopyStrategy_MissingFilesSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Empty host config directory -- no agents, skills, plugins, commands, settings.json
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	project, agent := "test", harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, project, agent)

	claudeCode := &config.ClaudeCodeConfig{
		Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
		UseHostAuth: boolPtr(false),
	}
	err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
		ProjectName:      project,
		AgentName:        agent,
		ContainerWorkDir: "/workspace",
		ClaudeCode:       claudeCode,
		CopyToVolume:     client.CopyToVolume,
	})
	require.NoError(t, err, "InitContainerConfig should succeed with empty host dir")

	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
	)

	// The .claude/ directory should exist (volume mount) but be mostly empty
	assert.True(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/"),
		".claude/ directory should exist")

	// None of the optional items should exist
	assert.False(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/agents/"),
		"agents/ should not exist")
	assert.False(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/skills/"),
		"skills/ should not exist")
	assert.False(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/plugins/"),
		"plugins/ should not exist")
	assert.False(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/commands/"),
		"commands/ should not exist")
	assert.False(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude/settings.json"),
		"settings.json should not exist")
}

// TestContainerFs_CopyStrategy_SymlinksResolved verifies that symlinked files and directories
// are resolved (dereferenced) before being copied into the container.
func TestContainerFs_CopyStrategy_SymlinksResolved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	hostDir := t.TempDir()

	// Create a real agents directory elsewhere
	realAgentsDir := filepath.Join(t.TempDir(), "real-agents")
	require.NoError(t, os.MkdirAll(realAgentsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realAgentsDir, "agent.md"), []byte("# Linked Agent"), 0o644))

	// Symlink agents/ in the host config dir
	require.NoError(t, os.Symlink(realAgentsDir, filepath.Join(hostDir, "agents")))

	// Create a real settings.json elsewhere and symlink it
	realSettings := filepath.Join(t.TempDir(), "real-settings.json")
	settingsData, _ := json.Marshal(map[string]any{
		"enabledPlugins": map[string]any{"linked-plugin": true},
	})
	require.NoError(t, os.WriteFile(realSettings, settingsData, 0o644))
	require.NoError(t, os.Symlink(realSettings, filepath.Join(hostDir, "settings.json")))

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	project, agent := "test", harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, project, agent)

	claudeCode := &config.ClaudeCodeConfig{
		Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
		UseHostAuth: boolPtr(false),
	}
	err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
		ProjectName:      project,
		AgentName:        agent,
		ContainerWorkDir: "/workspace",
		ClaudeCode:       claudeCode,
		CopyToVolume:     client.CopyToVolume,
	})
	require.NoError(t, err)

	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
	)

	t.Run("agents_is_real_directory_not_symlink", func(t *testing.T) {
		assert.True(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/agents/"),
			"agents/ should exist in the container")

		// Verify it is NOT a symlink (test -L returns 0 for symlinks)
		result, err := ctr.Exec(ctx, client, "test", "-L", containerHomeDir+"/.claude/agents")
		require.NoError(t, err)
		assert.NotEqual(t, 0, result.ExitCode,
			"agents/ should not be a symlink in the container")

		// Verify content was resolved
		content, err := ctr.ReadFile(ctx, client, containerHomeDir+"/.claude/agents/agent.md")
		require.NoError(t, err)
		assert.Equal(t, "# Linked Agent", content)
	})

	t.Run("settings_is_real_file_not_symlink", func(t *testing.T) {
		settingsPath := containerHomeDir + "/.claude/settings.json"
		assert.True(t, ctr.FileExists(ctx, client, settingsPath),
			"settings.json should exist in the container")

		// Verify it is NOT a symlink
		result, err := ctr.Exec(ctx, client, "test", "-L", settingsPath)
		require.NoError(t, err)
		assert.NotEqual(t, 0, result.ExitCode,
			"settings.json should not be a symlink in the container")
	})
}

// ---------------------------------------------------------------------------
// Rule: Fresh strategy produces a minimal or empty Claude Code config
// ---------------------------------------------------------------------------

func TestContainerFs_FreshStrategy_InContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// --- Scenario: Fresh strategy with host auth creates only credentials ---
	t.Run("fresh_with_host_auth_creates_only_credentials", func(t *testing.T) {
		hostDir := t.TempDir()
		seedCredentialsFile(t, hostDir)
		t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

		project, agent := "test", harness.UniqueAgentName(t)
		volumeName := createConfigVolume(t, ctx, client, project, agent)

		// Fresh strategy + host auth via production InitContainerConfig
		claudeCode := &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: boolPtr(true),
		}
		err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
			ProjectName:      project,
			AgentName:        agent,
			ContainerWorkDir: "/workspace",
			ClaudeCode:       claudeCode,
			CopyToVolume:     client.CopyToVolume,
		})
		require.NoError(t, err, "InitContainerConfig failed")

		ctr := harness.RunContainer(t, client, image,
			harness.WithCmd("sleep", "infinity"),
			harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
		)

		// .claude/ should exist (volume mount)
		assert.True(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/"),
			".claude/ should exist")

		// .credentials.json should be present and readable by claude user
		assert.True(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude/.credentials.json"),
			".credentials.json should exist")

		// Verify credentials readable by claude user (UID 1001)
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/.claude/.credentials.json")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode,
			".credentials.json should be readable; stderr=%s", result.Stderr)

		// Verify .credentials.json is the ONLY file in .claude/ (BDD: "should be the only file added")
		lsResult, err := ctr.Exec(ctx, client, "ls", "-A", containerHomeDir+"/.claude/")
		require.NoError(t, err)
		assert.Equal(t, 0, lsResult.ExitCode)
		assert.Equal(t, ".credentials.json", strings.TrimSpace(lsResult.Stdout),
			".credentials.json should be the only file in .claude/")
	})

	// --- Scenario: Fresh strategy without host auth creates empty config ---
	t.Run("fresh_without_host_auth_empty_config", func(t *testing.T) {
		project, agent := "test", harness.UniqueAgentName(t)
		volumeName := createConfigVolume(t, ctx, client, project, agent)

		// Fresh + no auth: InitContainerConfig should be a no-op
		claudeCode := &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: boolPtr(false),
		}
		err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
			ProjectName:      project,
			AgentName:        agent,
			ContainerWorkDir: "/workspace",
			ClaudeCode:       claudeCode,
			CopyToVolume:     client.CopyToVolume,
		})
		require.NoError(t, err, "InitContainerConfig should succeed as no-op")

		ctr := harness.RunContainer(t, client, image,
			harness.WithCmd("sleep", "infinity"),
			harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
		)

		// Volume mount creates .claude/ but it should be empty
		assert.True(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/"),
			".claude/ should exist (volume mount)")

		result, err := ctr.Exec(ctx, client, "ls", "-A", containerHomeDir+"/.claude/")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
		assert.Empty(t, strings.TrimSpace(result.Stdout), ".claude/ should be empty")
	})
}

// ---------------------------------------------------------------------------
// Rule: Host auth provisions onboarding and credential files
// ---------------------------------------------------------------------------

func TestContainerFs_HostAuth_OnboardingAndCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// --- Scenario: Onboarding marker is written via production InjectOnboardingFile ---
	t.Run("onboarding_marker_written", func(t *testing.T) {
		ctr := harness.RunContainer(t, client, image,
			harness.WithCmd("sleep", "infinity"),
		)

		// Inject onboarding via production code path
		err := shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{
			ContainerID:     ctr.ID,
			CopyToContainer: shared.NewCopyToContainerFn(client),
		})
		require.NoError(t, err, "InjectOnboardingFile failed")

		// Verify .claude.json exists
		claudeJSONPath := containerHomeDir + "/.claude.json"
		assert.True(t, ctr.FileExists(ctx, client, claudeJSONPath),
			".claude.json should exist in the container")

		// Verify contents
		content, err := ctr.ReadFile(ctx, client, claudeJSONPath)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &parsed), "unmarshal .claude.json")

		val, ok := parsed["hasCompletedOnboarding"]
		assert.True(t, ok, ".claude.json should have hasCompletedOnboarding key")
		assert.Equal(t, true, val, "hasCompletedOnboarding should be true")
	})

	// --- Scenario: Credentials via production InitContainerConfig ---
	t.Run("credentials_file_written", func(t *testing.T) {
		hostDir := t.TempDir()
		seedCredentialsFile(t, hostDir)
		t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

		project, agent := "test", harness.UniqueAgentName(t)
		volumeName := createConfigVolume(t, ctx, client, project, agent)

		claudeCode := &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: boolPtr(true),
		}
		err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
			ProjectName:      project,
			AgentName:        agent,
			ContainerWorkDir: "/workspace",
			ClaudeCode:       claudeCode,
			CopyToVolume:     client.CopyToVolume,
		})
		require.NoError(t, err, "InitContainerConfig failed")

		ctr := harness.RunContainer(t, client, image,
			harness.WithCmd("sleep", "infinity"),
			harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
		)

		// Verify .credentials.json exists and has correct structure
		credsPath := containerHomeDir + "/.claude/.credentials.json"
		assert.True(t, ctr.FileExists(ctx, client, credsPath),
			".credentials.json should exist in the container")

		content, err := ctr.ReadFile(ctx, client, credsPath)
		require.NoError(t, err)
		var creds map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &creds), "unmarshal .credentials.json")

		// Should contain claudeAiOauth with expected keys
		oauthRaw, ok := creds["claudeAiOauth"]
		require.True(t, ok, "credentials should contain claudeAiOauth")
		oauth, ok := oauthRaw.(map[string]any)
		require.True(t, ok, "claudeAiOauth should be an object")

		expectedKeys := []struct {
			key     string
			typeStr string
		}{
			{"accessToken", "string"},
			{"refreshToken", "string"},
			{"expiresAt", "number"},
			{"scopes", "array"},
			{"subscriptionType", "string"},
			{"rateLimitTier", "string"},
		}

		for _, kv := range expectedKeys {
			val, exists := oauth[kv.key]
			assert.True(t, exists, "claudeAiOauth should contain key %q", kv.key)
			if !exists {
				continue
			}

			switch kv.typeStr {
			case "string":
				_, ok := val.(string)
				assert.True(t, ok, "claudeAiOauth.%s should be a string, got %T", kv.key, val)
			case "number":
				_, ok := val.(float64)
				assert.True(t, ok, "claudeAiOauth.%s should be a number, got %T", kv.key, val)
			case "array":
				_, ok := val.([]any)
				assert.True(t, ok, "claudeAiOauth.%s should be an array, got %T", kv.key, val)
			}
		}

		// Should contain organizationUuid as string
		orgUUID, ok := creds["organizationUuid"]
		assert.True(t, ok, "credentials should contain organizationUuid")
		_, isStr := orgUUID.(string)
		assert.True(t, isStr, "organizationUuid should be a string")
	})
}

// ---------------------------------------------------------------------------
// Rule: Disabled host auth leaves no credential artifacts
// ---------------------------------------------------------------------------

func TestContainerFs_HostAuth_Disabled_NoArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
	)

	// When no init functions are called, no auth artifacts should exist.
	assert.False(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude.json"),
		".claude.json should NOT exist when host auth is disabled")

	assert.False(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude/.credentials.json"),
		".credentials.json should NOT exist when host auth is disabled")
}

// ---------------------------------------------------------------------------
// Rule: The copy strategy must resolve the host config directory before copying
// ---------------------------------------------------------------------------

func TestContainerFs_ConfigSourceResolution(t *testing.T) {
	// These scenarios test the containerfs.ResolveHostConfigDir function directly.
	// They do not require Docker, but are placed here for BDD completeness.

	t.Run("env_var_CLAUDE_CONFIG_DIR_takes_precedence", func(t *testing.T) {
		customDir := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", customDir)

		got, err := containerfs.ResolveHostConfigDir()
		require.NoError(t, err)
		assert.Equal(t, customDir, got,
			"should resolve to CLAUDE_CONFIG_DIR")
	})

	t.Run("falls_back_to_home_claude_dir", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "")

		home, err := os.UserHomeDir()
		require.NoError(t, err)

		claudeDir := filepath.Join(home, ".claude")
		if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
			t.Skip("~/.claude/ does not exist on this machine")
		}

		got, err := containerfs.ResolveHostConfigDir()
		require.NoError(t, err)
		assert.Equal(t, claudeDir, got,
			"should fall back to ~/.claude/")
	})

	t.Run("fails_when_no_config_dir_found", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		t.Setenv("HOME", t.TempDir())

		_, err := containerfs.ResolveHostConfigDir()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "claude config dir not found",
			"error should mention config dir not found")
	})

	t.Run("fails_when_CLAUDE_CONFIG_DIR_path_invalid", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/no/such/dir-containerfs-test")

		_, err := containerfs.ResolveHostConfigDir()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CLAUDE_CONFIG_DIR is set to",
			"error should reference the env var")
		assert.Contains(t, err.Error(), "path is invalid",
			"error should indicate path is invalid")
	})
}

// ---------------------------------------------------------------------------
// Rule: Full pipeline integration — copy strategy + host auth
// ---------------------------------------------------------------------------

func TestContainerFs_FullPipeline_CopyStrategy_WithHostAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Seed host config dir with full content
	hostDir := t.TempDir()
	seedHostConfigDir(t, hostDir)
	seedCredentialsFile(t, hostDir)
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	// Create config volume and init via production code path
	project, agent := "test", harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, project, agent)

	claudeCode := &config.ClaudeCodeConfig{
		Config:      config.ClaudeCodeConfigOptions{Strategy: "copy"},
		UseHostAuth: boolPtr(true),
	}
	err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
		ProjectName:      project,
		AgentName:        agent,
		ContainerWorkDir: "/workspace",
		ClaudeCode:       claudeCode,
		CopyToVolume:     client.CopyToVolume,
	})
	require.NoError(t, err, "InitContainerConfig failed")

	// Run container with config volume mounted
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
	)

	// Inject onboarding via production code path
	err = shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{
		ContainerID:     ctr.ID,
		CopyToContainer: shared.NewCopyToContainerFn(client),
	})
	require.NoError(t, err, "InjectOnboardingFile failed")

	// Verify the full state of the container
	t.Run("onboarding_marker_present", func(t *testing.T) {
		assert.True(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude.json"),
			".claude.json should exist")

		content, err := ctr.ReadFile(ctx, client, containerHomeDir+"/.claude.json")
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &parsed))
		assert.Equal(t, true, parsed["hasCompletedOnboarding"])
	})

	t.Run("credentials_present", func(t *testing.T) {
		credsPath := containerHomeDir + "/.claude/.credentials.json"
		assert.True(t, ctr.FileExists(ctx, client, credsPath),
			".credentials.json should exist")

		content, err := ctr.ReadFile(ctx, client, credsPath)
		require.NoError(t, err)
		var creds map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &creds))

		_, hasOAuth := creds["claudeAiOauth"]
		assert.True(t, hasOAuth, "should have claudeAiOauth")
		_, hasOrg := creds["organizationUuid"]
		assert.True(t, hasOrg, "should have organizationUuid")
	})

	t.Run("credentials_readable_by_claude_user", func(t *testing.T) {
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/.claude/.credentials.json")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode,
			".credentials.json should be readable by claude user (UID 1001); stderr=%s", result.Stderr)
	})

	t.Run("config_directories_present", func(t *testing.T) {
		for _, dir := range []string{"agents", "skills", "commands"} {
			assert.True(t, ctr.DirExists(ctx, client,
				containerHomeDir+"/.claude/"+dir+"/"),
				"%s/ should be present", dir)
		}
	})

	t.Run("plugins_present_with_cache", func(t *testing.T) {
		assert.True(t, ctr.DirExists(ctx, client,
			containerHomeDir+"/.claude/plugins/"),
			"plugins/ should be present")
		assert.True(t, ctr.DirExists(ctx, client,
			containerHomeDir+"/.claude/plugins/some-plugin/"),
			"some-plugin/ should be present")
		assert.True(t, ctr.DirExists(ctx, client,
			containerHomeDir+"/.claude/plugins/cache/"),
			"cache/ should be present")
	})

	t.Run("settings_only_enabledPlugins", func(t *testing.T) {
		content, err := ctr.ReadFile(ctx, client,
			containerHomeDir+"/.claude/settings.json")
		require.NoError(t, err)
		var settings map[string]any
		require.NoError(t, json.Unmarshal([]byte(content), &settings))

		_, hasPlugins := settings["enabledPlugins"]
		assert.True(t, hasPlugins, "should have enabledPlugins")
		assert.Len(t, settings, 1,
			"settings.json should only contain enabledPlugins, got keys: %v", keysOf(settings))
	})
}

// ---------------------------------------------------------------------------
// Rule: Full pipeline — fresh strategy + host auth
// ---------------------------------------------------------------------------

func TestContainerFs_FullPipeline_FreshStrategy_WithHostAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Fresh strategy with host auth: only credentials + onboarding, no config copy
	hostDir := t.TempDir()
	seedCredentialsFile(t, hostDir)
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)

	project, agent := "test", harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, project, agent)

	claudeCode := &config.ClaudeCodeConfig{
		Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
		UseHostAuth: boolPtr(true),
	}
	err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
		ProjectName:      project,
		AgentName:        agent,
		ContainerWorkDir: "/workspace",
		ClaudeCode:       claudeCode,
		CopyToVolume:     client.CopyToVolume,
	})
	require.NoError(t, err, "InitContainerConfig failed")

	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithVolumeMount(volumeName, containerHomeDir+"/.claude"),
	)

	// Inject onboarding via production code path
	err = shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{
		ContainerID:     ctr.ID,
		CopyToContainer: shared.NewCopyToContainerFn(client),
	})
	require.NoError(t, err, "InjectOnboardingFile failed")

	// Verify onboarding marker
	t.Run("onboarding_marker", func(t *testing.T) {
		assert.True(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude.json"))
	})

	// Verify credentials present and readable
	t.Run("credentials_present", func(t *testing.T) {
		assert.True(t, ctr.FileExists(ctx, client,
			containerHomeDir+"/.claude/.credentials.json"))
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/.claude/.credentials.json")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode,
			".credentials.json should be readable; stderr=%s", result.Stderr)
	})

	// Verify NO config dirs (fresh strategy = no config copy)
	t.Run("no_config_directories", func(t *testing.T) {
		for _, dir := range []string{"agents", "skills", "commands", "plugins"} {
			assert.False(t, ctr.DirExists(ctx, client,
				containerHomeDir+"/.claude/"+dir+"/"),
				"%s/ should NOT exist in fresh strategy", dir)
		}
	})

	// Verify NO settings.json
	t.Run("no_settings", func(t *testing.T) {
		assert.False(t, ctr.FileExists(ctx, client,
			containerHomeDir+"/.claude/settings.json"),
			"settings.json should NOT exist in fresh strategy")
	})
}

// ---------------------------------------------------------------------------
// Rule: Full pipeline — fresh strategy without host auth
// ---------------------------------------------------------------------------

func TestContainerFs_FullPipeline_FreshStrategy_NoHostAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Fresh strategy without host auth: completely clean container.
	// Even without calling InitContainerConfig, .claude/ exists from the image.
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
	)

	assert.False(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude.json"),
		".claude.json should NOT exist")
	assert.True(t, ctr.DirExists(ctx, client, containerHomeDir+"/.claude/"),
		".claude/ should exist (created by image)")
	// Verify it's empty — no config, no credentials
	result, err := ctr.Exec(ctx, client, "ls", "-A", containerHomeDir+"/.claude/")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, strings.TrimSpace(result.Stdout), ".claude/ should be empty")
}

// ---------------------------------------------------------------------------
// Rule: post_init script runs once on first start, skipped on restart
// ---------------------------------------------------------------------------

func TestContainerFs_AgentPostInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Parse post_init from YAML literal block — proves the full YAML multiline
	// string → config struct → PreparePostInitTar → container execution pipeline.
	parsedCfg, err := harness.ParseYAML[config.Project](`
agent:
  post_init: |
    echo "line1" >> ~/test.txt
    echo "line2" >> ~/test.txt
`)
	require.NoError(t, err)

	// Wire the YAML-parsed value into a config test double
	cfg := configtest.NewProjectBuilder().
		WithProject("test").
		WithAgent(config.AgentConfig{
			PostInit: parsedCfg.Agent.PostInit,
		}).
		Build()

	agent := harness.UniqueAgentName(t)
	volumeName := createConfigVolume(t, ctx, client, cfg.Project, agent)

	// Create container manually (not via RunContainer — we need to inject before start)
	containerName := fmt.Sprintf("clawker-test-postinit-%s", agent)
	labels := harness.AddTestLabels(map[string]string{
		harness.ClawkerManagedLabel: "true",
	})

	createResp, err := client.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config: &container.Config{
			Image:      image,
			Entrypoint: []string{"/usr/local/bin/entrypoint.sh"},
			Cmd:        []string{"sleep", "infinity"},
			Labels:     labels,
		},
		HostConfig: &container.HostConfig{
			CapAdd: []string{"NET_ADMIN", "NET_RAW"},
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: volumeName,
					Target: containerHomeDir + "/.claude",
				},
			},
		},
		Name: containerName,
	})
	require.NoError(t, err, "ContainerCreate failed")

	containerID := createResp.ID
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := client.ContainerStop(cleanupCtx, containerID, nil); err != nil {
			t.Logf("WARNING: failed to stop container %s: %v", containerName, err)
		}
		if _, err := client.ContainerRemove(cleanupCtx, containerID, true); err != nil {
			t.Logf("WARNING: failed to remove container %s: %v", containerName, err)
		}
	})

	// Inject post-init script via production code path, using config's PostInit value
	err = shared.InjectPostInitScript(ctx, shared.InjectPostInitOpts{
		ContainerID:     containerID,
		Script:          cfg.Agent.PostInit,
		CopyToContainer: shared.NewCopyToContainerFn(client),
	})
	require.NoError(t, err, "InjectPostInitScript failed")

	// Start container — entrypoint runs, finds post-init.sh, executes it
	_, err = client.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: containerID})
	require.NoError(t, err, "ContainerStart failed")

	ctr := &harness.RunningContainer{ID: containerID, Name: containerName}

	// Wait for ready file (entrypoint signals readiness after post-init)
	err = harness.WaitForReadyFile(ctx, client, containerID)
	if err != nil {
		logs, logErr := harness.GetContainerLogs(ctx, client, containerID)
		if logErr != nil {
			t.Logf("failed to get logs: %v", logErr)
		} else {
			t.Logf("container logs:\n%s", logs)
		}
		require.NoError(t, err, "container did not become ready")
	}

	// --- Scenario: Post-init script ran and produced output ---
	t.Run("post_init_output_created", func(t *testing.T) {
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/test.txt")
		require.NoError(t, err, "reading test.txt")
		require.Equal(t, 0, result.ExitCode, "cat failed: %s", result.Stderr)
		assert.Equal(t, "line1\nline2\n", result.Stdout,
			"test.txt should contain exactly the post-init output")
	})

	// --- Scenario: Marker file exists on config volume ---
	t.Run("marker_file_created", func(t *testing.T) {
		assert.True(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude/post-initialized"),
			"post-initialized marker should exist on config volume")
	})

	// --- Scenario: Restart does NOT re-run post-init ---
	t.Run("restart_skips_post_init", func(t *testing.T) {
		// Stop container (exit code 137/SIGKILL is expected from docker stop)
		_, err := client.ContainerStop(ctx, containerID, nil)
		require.NoError(t, err, "ContainerStop failed")

		// Poll until container is not running (ignore exit code — stop sends SIGKILL)
		require.Eventually(t, func() bool {
			info, inspectErr := client.ContainerInspect(ctx, containerID, docker.ContainerInspectOptions{})
			return inspectErr == nil && !info.Container.State.Running
		}, 30*time.Second, 200*time.Millisecond, "container did not stop")

		// Restart
		_, err = client.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: containerID})
		require.NoError(t, err, "ContainerStart (restart) failed")

		// Wait for ready again
		err = harness.WaitForReadyFile(ctx, client, containerID)
		require.NoError(t, err, "container did not become ready after restart")

		// Verify test.txt still has exactly the same content (not duplicated)
		result, err := ctr.Exec(ctx, client, "cat", containerHomeDir+"/test.txt")
		require.NoError(t, err, "reading test.txt after restart")
		require.Equal(t, 0, result.ExitCode, "cat failed: %s", result.Stderr)
		assert.Equal(t, "line1\nline2\n", result.Stdout,
			"test.txt should NOT have duplicated content after restart")

		// Marker still exists
		assert.True(t, ctr.FileExists(ctx, client, containerHomeDir+"/.claude/post-initialized"),
			"post-initialized marker should still exist after restart")
	})
}

// ---------------------------------------------------------------------------
// Helpers for test assertions
// ---------------------------------------------------------------------------

// keysOf returns the keys of a map as a sorted slice.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
