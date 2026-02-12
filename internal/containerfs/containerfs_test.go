package containerfs

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/keyring"
)

// ---------------------------------------------------------------------------
// ResolveHostConfigDir
// ---------------------------------------------------------------------------

func TestResolveHostConfigDir_EnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	got, err := ResolveHostConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveHostConfigDir_EnvVarMissing(t *testing.T) {
	// env var points to non-existent dir → should return error (not fall through)
	t.Setenv("CLAUDE_CONFIG_DIR", "/no/such/dir-containerfs-test")

	_, err := ResolveHostConfigDir()
	if err == nil {
		t.Fatal("expected error when CLAUDE_CONFIG_DIR is set to non-existent path, got nil")
	}
	if !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR is set to") {
		t.Errorf("error should mention CLAUDE_CONFIG_DIR, got: %v", err)
	}
}

func TestResolveHostConfigDir_DefaultFallback(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "") // unset

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("get home dir: %v", err)
	}
	claudeDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		t.Skip("~/.claude/ does not exist on this machine")
	}

	got, err := ResolveHostConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != claudeDir {
		t.Errorf("got %q, want %q", got, claudeDir)
	}
}

func TestResolveHostConfigDir_NeitherExists(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/no/such/dir-containerfs-test")
	// We need HOME to point to a dir without .claude/ in it
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	_, err := ResolveHostConfigDir()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// PrepareClaudeConfig
// ---------------------------------------------------------------------------

func TestPrepareClaudeConfig_SettingsJSON(t *testing.T) {
	hostDir := t.TempDir()
	hostSettings := map[string]any{
		"enabledPlugins": map[string]any{
			"plugin-a": true,
			"plugin-b": false,
		},
		"someOtherKey": "should-not-be-copied",
		"anotherKey":   42,
	}
	writeJSON(t, filepath.Join(hostDir, "settings.json"), hostSettings)

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	staged := filepath.Join(stagingDir, ".claude", "settings.json")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged settings.json: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal staged settings: %v", err)
	}

	// Should have enabledPlugins
	if _, ok := result["enabledPlugins"]; !ok {
		t.Error("missing enabledPlugins in staged settings")
	}
	// Should NOT have other keys
	if _, ok := result["someOtherKey"]; ok {
		t.Error("unexpected someOtherKey in staged settings")
	}
	if _, ok := result["anotherKey"]; ok {
		t.Error("unexpected anotherKey in staged settings")
	}
}

func TestPrepareClaudeConfig_DirectoriesCopied(t *testing.T) {
	hostDir := t.TempDir()

	// Create agents/, skills/, commands/ with test files
	for _, dir := range []string{"agents", "skills", "commands"} {
		dirPath := filepath.Join(hostDir, dir)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dirPath, "test.txt"), []byte("content-"+dir), 0o644); err != nil {
			t.Fatalf("write %s/test.txt: %v", dir, err)
		}
	}

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	for _, dir := range []string{"agents", "skills", "commands"} {
		staged := filepath.Join(stagingDir, ".claude", dir, "test.txt")
		data, err := os.ReadFile(staged)
		if err != nil {
			t.Fatalf("read staged %s/test.txt: %v", dir, err)
		}
		want := "content-" + dir
		if string(data) != want {
			t.Errorf("%s/test.txt: got %q, want %q", dir, string(data), want)
		}
	}
}

func TestPrepareClaudeConfig_PluginsCacheCopied(t *testing.T) {
	hostDir := t.TempDir()

	pluginsDir := filepath.Join(hostDir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginsDir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginsDir, "my-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "cache", "cached.dat"), []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "install-counts-cache.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "my-plugin", "plugin.js"), []byte("plugin"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// my-plugin should be copied
	staged := filepath.Join(stagingDir, ".claude", "plugins", "my-plugin", "plugin.js")
	if _, err := os.Stat(staged); os.IsNotExist(err) {
		t.Error("my-plugin/plugin.js should be copied but was not found")
	}

	// cache/ SHOULD be copied (needed for plugin assets)
	cachedFile := filepath.Join(stagingDir, ".claude", "plugins", "cache", "cached.dat")
	if _, err := os.Stat(cachedFile); os.IsNotExist(err) {
		t.Error("plugins/cache/cached.dat should be copied but was not found")
	}

	// install-counts-cache.json should NOT be copied
	installCounts := filepath.Join(stagingDir, ".claude", "plugins", "install-counts-cache.json")
	if _, err := os.Stat(installCounts); !os.IsNotExist(err) {
		t.Error("install-counts-cache.json should be excluded but exists in staging")
	}
}

func TestPrepareClaudeConfig_KnownMarketplacesRewrite(t *testing.T) {
	hostDir := t.TempDir()

	pluginsDir := filepath.Join(hostDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Both installPath and installLocation use host config dir paths
	marketplace := []map[string]any{
		{
			"name":            "test-plugin",
			"installPath":     filepath.Join(hostDir, "plugins", "cache", "test-marketplace", "test-plugin", "1.0.0"),
			"installLocation": filepath.Join(hostDir, "plugins", "marketplaces", "test-marketplace"),
		},
		{
			"name":            "another-plugin",
			"installPath":     filepath.Join(hostDir, "plugins", "cache", "another-marketplace", "another-plugin", "2.0"),
			"installLocation": filepath.Join(hostDir, "plugins", "marketplaces", "another-marketplace"),
			"version":         "2.0",
		},
	}
	writeJSON(t, filepath.Join(pluginsDir, "known_marketplaces.json"), marketplace)

	containerHome := "/home/claude"
	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, containerHome, "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	staged := filepath.Join(stagingDir, ".claude", "plugins", "known_marketplaces.json")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged known_marketplaces.json: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	containerPluginsPrefix := filepath.Join(containerHome, ".claude", "plugins")

	for _, entry := range result {
		name := entry["name"].(string)

		// Check installPath was rewritten
		if ip, ok := entry["installPath"].(string); ok {
			if !strings.HasPrefix(ip, containerPluginsPrefix) {
				t.Errorf("entry %q: installPath not rewritten: got %q, want prefix %q", name, ip, containerPluginsPrefix)
			}
			if strings.Contains(ip, hostDir) {
				t.Errorf("entry %q: installPath still contains host path: %q", name, ip)
			}
		}

		// Check installLocation was rewritten
		if il, ok := entry["installLocation"].(string); ok {
			if !strings.HasPrefix(il, containerPluginsPrefix) {
				t.Errorf("entry %q: installLocation not rewritten: got %q, want prefix %q", name, il, containerPluginsPrefix)
			}
			if strings.Contains(il, hostDir) {
				t.Errorf("entry %q: installLocation still contains host path: %q", name, il)
			}
		}
	}
}

func TestPrepareClaudeConfig_InstalledPluginsRewrite(t *testing.T) {
	hostDir := t.TempDir()

	pluginsDir := filepath.Join(hostDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed installed_plugins.json with host-absolute paths
	installed := []map[string]any{
		{
			"name":        "my-plugin",
			"installPath": filepath.Join(hostDir, "plugins", "cache", "my-marketplace", "my-plugin", "1.0.0"),
			"projectPath": "/Users/andrew/Code/myproject",
		},
		{
			"name":        "another-plugin",
			"installPath": filepath.Join(hostDir, "plugins", "cache", "another-marketplace", "another-plugin", "2.0"),
		},
	}
	writeJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), installed)

	containerHome := "/home/claude"
	containerWorkDir := "/workspace"
	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, containerHome, containerWorkDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	staged := filepath.Join(stagingDir, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged installed_plugins.json: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	containerPluginsPrefix := filepath.Join(containerHome, ".claude", "plugins")

	for _, entry := range result {
		name := entry["name"].(string)

		// installPath should be rewritten with container prefix
		if ip, ok := entry["installPath"].(string); ok {
			if !strings.HasPrefix(ip, containerPluginsPrefix) {
				t.Errorf("entry %q: installPath not rewritten: got %q, want prefix %q", name, ip, containerPluginsPrefix)
			}
			if strings.Contains(ip, hostDir) {
				t.Errorf("entry %q: installPath still contains host path: %q", name, ip)
			}
		}

		// projectPath should be replaced entirely with containerWorkDir
		if pp, ok := entry["projectPath"].(string); ok {
			if pp != containerWorkDir {
				t.Errorf("entry %q: projectPath not replaced: got %q, want %q", name, pp, containerWorkDir)
			}
		}
	}
}

func TestPrepareClaudeConfig_MissingFilesSkipped(t *testing.T) {
	hostDir := t.TempDir()
	// Empty host dir — nothing to copy

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Should succeed with an empty .claude/ dir
	claudeDir := filepath.Join(stagingDir, ".claude")
	info, err := os.Stat(claudeDir)
	if err != nil {
		t.Fatalf("staging .claude/ should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("staging .claude/ should be a directory")
	}
}

func TestPrepareClaudeConfig_SymlinksResolved(t *testing.T) {
	hostDir := t.TempDir()

	// Create a real directory with a file
	realDir := filepath.Join(t.TempDir(), "real-agents")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "agent.txt"), []byte("agent-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink in the host dir
	if err := os.Symlink(realDir, filepath.Join(hostDir, "agents")); err != nil {
		t.Fatal(err)
	}

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Staged agents/ should be a real directory (not a symlink)
	stagedAgents := filepath.Join(stagingDir, ".claude", "agents")
	info, err := os.Lstat(stagedAgents)
	if err != nil {
		t.Fatalf("stat staged agents: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("staged agents/ should not be a symlink")
	}

	// Contents should be copied
	data, err := os.ReadFile(filepath.Join(stagedAgents, "agent.txt"))
	if err != nil {
		t.Fatalf("read staged agent.txt: %v", err)
	}
	if string(data) != "agent-content" {
		t.Errorf("agent.txt: got %q, want %q", string(data), "agent-content")
	}
}

// ---------------------------------------------------------------------------
// PrepareCredentials
// ---------------------------------------------------------------------------

func TestPrepareCredentials_FromKeyring(t *testing.T) {
	keyring.MockInit()

	current, err := user.Current()
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}

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

	if err := keyring.Set("Claude Code-credentials", current.Username, validJSON); err != nil {
		t.Fatalf("seed keyring: %v", err)
	}

	hostDir := t.TempDir() // not used because keyring succeeds

	stagingDir, cleanup, err := PrepareCredentials(hostDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	credFile := filepath.Join(stagingDir, ".claude", ".credentials.json")
	data, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}

	var creds map[string]any
	if err := json.Unmarshal(data, &creds); err != nil {
		t.Fatalf("unmarshal credentials: %v", err)
	}

	if _, ok := creds["claudeAiOauth"]; !ok {
		t.Error("credentials should contain claudeAiOauth")
	}
}

func TestPrepareCredentials_FallbackToFile(t *testing.T) {
	keyring.MockInit() // mock keyring with no entries

	hostDir := t.TempDir()
	credContent := `{"claudeAiOauth":{"accessToken":"from-file"},"organizationUuid":"550e8400-e29b-41d4-a716-446655440000"}`
	if err := os.WriteFile(filepath.Join(hostDir, ".credentials.json"), []byte(credContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir, cleanup, err := PrepareCredentials(hostDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	credFile := filepath.Join(stagingDir, ".claude", ".credentials.json")
	data, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}

	if string(data) != credContent {
		t.Errorf("credentials content mismatch: got %q", string(data))
	}
}

func TestPrepareCredentials_NeitherSource(t *testing.T) {
	keyring.MockInit() // mock keyring with no entries

	hostDir := t.TempDir() // no .credentials.json file

	_, _, err := PrepareCredentials(hostDir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// PrepareOnboardingTar
// ---------------------------------------------------------------------------

func TestPrepareOnboardingTar(t *testing.T) {
	reader, err := PrepareOnboardingTar("/home/claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tr := tar.NewReader(reader)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar next: %v", err)
	}

	if hdr.Name != ".claude.json" {
		t.Errorf("tar entry name: got %q, want %q", hdr.Name, ".claude.json")
	}
	if hdr.Mode != 0o600 {
		t.Errorf("tar entry mode: got %#o, want %#o", hdr.Mode, int64(0o600))
	}
	if hdr.ModTime.IsZero() {
		t.Error("tar entry modtime should not be zero (epoch)")
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, tr); err != nil {
		t.Fatalf("read tar entry: %v", err)
	}

	var content map[string]any
	if err := json.Unmarshal(buf.Bytes(), &content); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	val, ok := content["hasCompletedOnboarding"]
	if !ok {
		t.Fatal("missing hasCompletedOnboarding")
	}
	if val != true {
		t.Errorf("hasCompletedOnboarding: got %v, want true", val)
	}

	// Should have no more entries
	_, err = tr.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got: %v", err)
	}
}

func TestPreparePostInitTar_EmptyScript(t *testing.T) {
	_, err := PreparePostInitTar("")
	if err == nil {
		t.Fatal("expected error for empty script")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}

	// Whitespace-only should also fail
	_, err = PreparePostInitTar("   \n\t  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only script")
	}
}

func TestPreparePostInitTar(t *testing.T) {
	script := "claude mcp add -- npx -y @anthropic-ai/claude-code-mcp\nnpm install -g typescript\n"

	reader, err := PreparePostInitTar(script)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tr := tar.NewReader(reader)

	// First entry: .clawker/ directory
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar next (dir): %v", err)
	}
	if hdr.Name != ".clawker/" {
		t.Errorf("first tar entry name: got %q, want %q", hdr.Name, ".clawker/")
	}
	if hdr.Typeflag != tar.TypeDir {
		t.Errorf("first tar entry type: got %d, want TypeDir (%d)", hdr.Typeflag, tar.TypeDir)
	}
	if hdr.Mode != 0o755 {
		t.Errorf("dir mode: got %#o, want %#o", hdr.Mode, int64(0o755))
	}
	if hdr.Uid != 1001 || hdr.Gid != 1001 {
		t.Errorf("dir uid/gid: got %d/%d, want 1001/1001", hdr.Uid, hdr.Gid)
	}

	// Second entry: .clawker/post-init.sh file
	hdr, err = tr.Next()
	if err != nil {
		t.Fatalf("tar next (file): %v", err)
	}
	if hdr.Name != ".clawker/post-init.sh" {
		t.Errorf("second tar entry name: got %q, want %q", hdr.Name, ".clawker/post-init.sh")
	}
	if hdr.Mode != 0o755 {
		t.Errorf("file mode: got %#o, want %#o", hdr.Mode, int64(0o755))
	}
	if hdr.Uid != 1001 || hdr.Gid != 1001 {
		t.Errorf("file uid/gid: got %d/%d, want 1001/1001", hdr.Uid, hdr.Gid)
	}
	if hdr.ModTime.IsZero() {
		t.Error("file modtime should not be zero")
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, tr); err != nil {
		t.Fatalf("read tar entry: %v", err)
	}

	content := buf.String()
	wantPrefix := "#!/bin/bash\nset -e\n"
	if !strings.HasPrefix(content, wantPrefix) {
		t.Errorf("script should start with shebang+set-e, got:\n%s", content)
	}
	if !strings.Contains(content, "claude mcp add") {
		t.Error("script should contain user commands")
	}
	if !strings.Contains(content, "npm install -g typescript") {
		t.Error("script should contain user commands")
	}

	// Should have no more entries
	_, err = tr.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestPrepareClaudeConfig_SettingsNoEnabledPlugins(t *testing.T) {
	hostDir := t.TempDir()
	writeJSON(t, filepath.Join(hostDir, "settings.json"), map[string]any{
		"theme": "dark",
	})

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// settings.json should NOT be created when enabledPlugins is missing
	staged := filepath.Join(stagingDir, ".claude", "settings.json")
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("settings.json should not be staged when enabledPlugins is absent")
	}
}

func TestPrepareClaudeConfig_NestedPluginDirs(t *testing.T) {
	hostDir := t.TempDir()
	pluginsDir := filepath.Join(hostDir, "plugins")

	// Create nested plugin structure: plugins/my-plugin/lib/util.js
	nested := filepath.Join(pluginsDir, "my-plugin", "lib")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "util.js"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create cache/nested/ — should now be included
	if err := os.MkdirAll(filepath.Join(pluginsDir, "cache", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "cache", "nested", "file.dat"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Nested plugin file should be copied
	staged := filepath.Join(stagingDir, ".claude", "plugins", "my-plugin", "lib", "util.js")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read nested plugin file: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("nested util.js: got %q, want %q", string(data), "nested")
	}

	// Nested cache SHOULD now be included
	cachedNested := filepath.Join(stagingDir, ".claude", "plugins", "cache", "nested", "file.dat")
	data, err = os.ReadFile(cachedNested)
	if err != nil {
		t.Fatalf("read cache/nested/file.dat: %v", err)
	}
	if string(data) != "cached" {
		t.Errorf("cache/nested/file.dat: got %q, want %q", string(data), "cached")
	}
}

func TestPrepareClaudeConfig_KnownMarketplacesNonStringInstallPath(t *testing.T) {
	hostDir := t.TempDir()
	pluginsDir := filepath.Join(hostDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Entry with numeric installPath (should be left alone, not cause crash)
	marketplace := []map[string]any{
		{
			"name":        "weird-plugin",
			"installPath": 42,
		},
	}
	writeJSON(t, filepath.Join(pluginsDir, "known_marketplaces.json"), marketplace)

	_, cleanup, err := PrepareClaudeConfig(hostDir, "/home/claude", "/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
}

func TestPrepareCredentials_NeitherSourceErrorMessage(t *testing.T) {
	keyring.MockInit()
	hostDir := t.TempDir()

	_, _, err := PrepareCredentials(hostDir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	want := "no claude code credentials found"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error message should contain %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
