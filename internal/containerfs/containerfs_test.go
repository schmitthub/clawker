package containerfs

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/logger"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// claudeSrc composes a claude-bundle-style src using the env-default
// expansion the real manifest uses.
func claudeSrc(rel string) string {
	return "${CLAUDE_CONFIG_DIR:-~/.claude}/" + rel
}

// claudeStaging mirrors the claude bundle's staging manifest — the shape
// these tests originally hardcoded. Keeping the fixture in claude's shape
// proves the manifest interpreter reproduces the legacy behavior.
func claudeStaging() harness.Staging {
	return harness.Staging{
		Copy: []harness.CopySpec{
			{
				Src: claudeSrc("settings.json"), Dest: ".claude/settings.json",
				JSONKeys: []string{"enabledPlugins"}, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: claudeSrc("CLAUDE.md"), Dest: ".claude/CLAUDE.md",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: claudeSrc("agents"), Dest: ".claude/agents",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: claudeSrc("skills"), Dest: ".claude/skills",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: claudeSrc("commands"), Dest: ".claude/commands",
				JSONKeys: nil, Skip: nil, JSONRewrites: nil,
			},
			{
				Src: claudeSrc("plugins"), Dest: ".claude/plugins",
				JSONKeys: nil,
				Skip:     []string{"install-counts-cache.json"},
				JSONRewrites: []harness.JSONRewrite{
					{File: "known_marketplaces.json", Key: "installPath", Rewrite: harness.RewritePrefixSwap},
					{File: "known_marketplaces.json", Key: "installLocation", Rewrite: harness.RewritePrefixSwap},
					{File: "installed_plugins.json", Key: "installPath", Rewrite: harness.RewritePrefixSwap},
					{File: "installed_plugins.json", Key: "projectPath", Rewrite: harness.RewriteReplaceWithWorkdir},
				},
			},
		},
		Mounts: []harness.MountSpec{{Src: claudeSrc("projects"), Dest: ".claude/projects"}},
	}
}

// ---------------------------------------------------------------------------
// ResolveHostProjectsDir
// ---------------------------------------------------------------------------

func TestResolveHostProjectsDir_Exists(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	projectsDir := filepath.Join(hostDir, "projects")
	if err := os.Mkdir(projectsDir, 0o700); err != nil {
		t.Fatalf("create projects dir: %v", err)
	}

	got, ok, err := ResolveHostMountSource(claudeSrc("projects"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got != projectsDir {
		t.Errorf("got %q, want %q", got, projectsDir)
	}
}

func TestResolveHostProjectsDir_Missing(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	// No projects subdir created.

	got, ok, err := ResolveHostMountSource(claudeSrc("projects"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (got path %q)", got)
	}
}

func TestResolveHostProjectsDir_IsFile(t *testing.T) {
	hostDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	if err := os.WriteFile(filepath.Join(hostDir, "projects"), []byte("nope"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, _, err := ResolveHostMountSource(claudeSrc("projects"))
	if err == nil {
		t.Fatal("expected error when projects exists as file, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got: %v", err)
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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

func TestPrepareClaudeConfig_CLAUDEMDCopied(t *testing.T) {
	hostDir := t.TempDir()

	content := "# My Custom Instructions\nAlways use tabs.\n"
	if err := os.WriteFile(filepath.Join(hostDir, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	staged := filepath.Join(stagingDir, ".claude", "CLAUDE.md")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read staged CLAUDE.md: %v", err)
	}
	if string(data) != content {
		t.Errorf("CLAUDE.md: got %q, want %q", string(data), content)
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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
			"name": "another-plugin",
			"installPath": filepath.Join(
				hostDir,
				"plugins",
				"cache",
				"another-marketplace",
				"another-plugin",
				"2.0",
			),
			"installLocation": filepath.Join(hostDir, "plugins", "marketplaces", "another-marketplace"),
			"version":         "2.0",
		},
	}
	writeJSON(t, filepath.Join(pluginsDir, "known_marketplaces.json"), marketplace)

	containerHome := "/home/claude"
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), containerHome, "/workspace", "")
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
				t.Errorf(
					"entry %q: installPath not rewritten: got %q, want prefix %q",
					name,
					ip,
					containerPluginsPrefix,
				)
			}
			if strings.Contains(ip, hostDir) {
				t.Errorf("entry %q: installPath still contains host path: %q", name, ip)
			}
		}

		// Check installLocation was rewritten
		if il, ok := entry["installLocation"].(string); ok {
			if !strings.HasPrefix(il, containerPluginsPrefix) {
				t.Errorf(
					"entry %q: installLocation not rewritten: got %q, want prefix %q",
					name,
					il,
					containerPluginsPrefix,
				)
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
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(
		logger.Nop(),
		claudeStaging(),
		containerHome,
		containerWorkDir,
		"",
	)
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
				t.Errorf(
					"entry %q: installPath not rewritten: got %q, want prefix %q",
					name,
					ip,
					containerPluginsPrefix,
				)
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Nothing staged: no .claude subtree materializes, and the volume-init
	// step downstream skips the volume copy for the missing subtree.
	if _, statErr := os.Stat(filepath.Join(stagingDir, ".claude")); !os.IsNotExist(statErr) {
		t.Errorf("expected no staged .claude subtree for an empty host dir, got stat err=%v", statErr)
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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

func TestPrepareClaudeConfig_BrokenSymlinkSkipped(t *testing.T) {
	hostDir := t.TempDir()

	// Plugins cache with a dangling symlink (stale plugin file removed by an
	// update) next to a valid file — mirrors Claude Code leaving broken cache
	// symlinks behind.
	cacheDir := filepath.Join(hostDir, "plugins", "cache", "my-plugin")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "good.md"), []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(hostDir, "plugins", "marketplaces", "gone"),
		filepath.Join(cacheDir, "SKILL.md"),
	); err != nil {
		t.Fatal(err)
	}

	// Same shape inside agents/ to exercise the copyDir path.
	agentsDir := filepath.Join(hostDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "agent.md"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(hostDir, "no-such-target"),
		filepath.Join(agentsDir, "dangling.md"),
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Valid files copied.
	if _, err := os.Stat(filepath.Join(stagingDir, ".claude", "plugins", "cache", "my-plugin", "good.md")); err != nil {
		t.Errorf("good.md should be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stagingDir, ".claude", "agents", "agent.md")); err != nil {
		t.Errorf("agent.md should be copied: %v", err)
	}

	// Broken symlinks skipped, not staged.
	if _, lstatErr := os.Lstat(
		filepath.Join(stagingDir, ".claude", "plugins", "cache", "my-plugin", "SKILL.md"),
	); !os.IsNotExist(lstatErr) {
		t.Error("broken plugin symlink should be skipped")
	}
	if _, err := os.Lstat(filepath.Join(stagingDir, ".claude", "agents", "dangling.md")); !os.IsNotExist(err) {
		t.Error("broken agents symlink should be skipped")
	}
}

// TestPrepareHookTar covers PrepareHookTar: a parameterized hook name
// (.clawker/<name>.sh) and an empty script producing a valid no-op wrapper
// instead of an error.
func TestPrepareHookTar(t *testing.T) {
	cfg := configmocks.NewBlankConfig()

	// readHookFile asserts the dir entry's header invariants, then returns
	// the file entry's name + body, asserting exactly one file follows.
	//
	// Mode 0755 is load-bearing, not a tautological echo of the constructor:
	// the agent init plan gates both hooks on the executable bit
	// (`[ -x "$POST" ]`) and execs the script directly (not `bash <file>`),
	// so a drop to 0644 silently no-ops the hook with no error surfaced
	// (post-init even writes its DONE marker and never retries). uid/gid are
	// asserted against cfg (not literal 1001) to exercise the config→header
	// binding the unprivileged container user depends on to own and exec it.
	readHookFile := func(t *testing.T, reader io.Reader) (string, string) {
		t.Helper()
		tr := tar.NewReader(reader)
		dirHdr, err := tr.Next() // .clawker/ dir
		if err != nil {
			t.Fatalf("tar next (dir): %v", err)
		}
		if dirHdr.Mode != 0o755 {
			t.Errorf("dir mode: got %#o, want %#o", dirHdr.Mode, 0o755)
		}
		if dirHdr.Uid != cfg.ContainerUID() || dirHdr.Gid != cfg.ContainerGID() {
			t.Errorf("dir uid/gid: got %d/%d, want %d/%d",
				dirHdr.Uid, dirHdr.Gid, cfg.ContainerUID(), cfg.ContainerGID())
		}
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("tar next (file): %v", err)
		}
		if hdr.Mode != 0o755 {
			t.Errorf("file mode: got %#o, want %#o (script must be executable)", hdr.Mode, 0o755)
		}
		if hdr.Uid != cfg.ContainerUID() || hdr.Gid != cfg.ContainerGID() {
			t.Errorf("file uid/gid: got %d/%d, want %d/%d",
				hdr.Uid, hdr.Gid, cfg.ContainerUID(), cfg.ContainerGID())
		}
		var buf bytes.Buffer
		// nosemgrep: go.lang.security.decompression_bomb.potential-dos-via-decompression-bomb
		if _, copyErr := io.Copy(&buf, tr); copyErr != nil {
			t.Fatalf("read tar entry: %v", copyErr)
		}
		if _, err := tr.Next(); err != io.EOF {
			t.Errorf("expected EOF after single file, got: %v", err)
		}
		return hdr.Name, buf.String()
	}

	// Named hook → .clawker/<name>.sh carrying the user body.
	reader, err := PrepareHookTar(cfg, "zsh", "npm install\n", "pre-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	name, content := readHookFile(t, reader)
	if name != ".clawker/pre-run.sh" {
		t.Errorf("file name: got %q, want .clawker/pre-run.sh", name)
	}
	if !strings.HasPrefix(content, "#!/bin/zsh\nset -e\n") {
		t.Errorf("missing shebang+set-e: %q", content)
	}
	if !strings.Contains(content, "npm install") {
		t.Error("body missing user script")
	}

	// Empty script → bare no-op wrapper, no error (lets the CLI always
	// deliver, overwriting any stale prior script when the hook is unset).
	reader, err = PrepareHookTar(cfg, "zsh", "", "pre-run")
	if err != nil {
		t.Fatalf("empty script should not error: %v", err)
	}
	name, content = readHookFile(t, reader)
	if name != ".clawker/pre-run.sh" {
		t.Errorf("file name: got %q, want .clawker/pre-run.sh", name)
	}
	if content != "#!/bin/zsh\nset -e\n" {
		t.Errorf("empty hook should be a bare no-op wrapper, got: %q", content)
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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
	if err := os.WriteFile(
		filepath.Join(pluginsDir, "cache", "nested", "file.dat"),
		[]byte("cached"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
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

	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	_, cleanup, err := PrepareConfig(logger.Nop(), claudeStaging(), "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
}

// ---------------------------------------------------------------------------
// Explicit-copy vocabulary: globs, renames, workspace guard
// ---------------------------------------------------------------------------

// copyOnly builds a staging manifest holding exactly one copy directive.
func copyOnly(c harness.CopySpec) harness.Staging {
	return harness.Staging{
		Copy:   []harness.CopySpec{c},
		Mounts: nil,
	}
}

func TestStageCopy_GlobFansOutUnderDest(t *testing.T) {
	hostDir := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.txt"} {
		if err := os.WriteFile(filepath.Join(hostDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	staging := copyOnly(harness.CopySpec{
		Src: claudeSrc("*.md"), Dest: ".claude/notes",
		JSONKeys: nil, Skip: nil, JSONRewrites: nil,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), staging, "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	for _, want := range []string{"a.md", "b.md"} {
		if _, statErr := os.Stat(filepath.Join(stagingDir, ".claude", "notes", want)); statErr != nil {
			t.Errorf("glob match %s not staged under dest: %v", want, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(stagingDir, ".claude", "notes", "c.txt")); !os.IsNotExist(statErr) {
		t.Error("c.txt must not match the *.md glob")
	}
}

func TestStageCopy_SingleLiteralSrcCopiesToExactDest(t *testing.T) {
	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "AGENTS.md"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rename in flight: dest basename differs from src basename.
	staging := copyOnly(harness.CopySpec{
		Src: claudeSrc("AGENTS.md"), Dest: ".claude/instructions.md",
		JSONKeys: nil, Skip: nil, JSONRewrites: nil,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	stagingDir, cleanup, err := PrepareConfig(logger.Nop(), staging, "/home/claude", "/workspace", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	data, readErr := os.ReadFile(filepath.Join(stagingDir, ".claude", "instructions.md"))
	if readErr != nil {
		t.Fatalf("renamed dest not staged: %v", readErr)
	}
	if string(data) != "global" {
		t.Errorf("staged content = %q, want %q", data, "global")
	}
}

func TestStageCopy_WorkspaceSrcRejected(t *testing.T) {
	hostDir := t.TempDir()
	projectRoot := filepath.Join(hostDir, "repo")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(projectRoot, "AGENTS.md")
	if err := os.WriteFile(inside, []byte("repo file"), 0o644); err != nil {
		t.Fatal(err)
	}

	staging := copyOnly(harness.CopySpec{
		Src: inside, Dest: ".claude/AGENTS.md",
		JSONKeys: nil, Skip: nil, JSONRewrites: nil,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", hostDir)
	_, _, err := PrepareConfig(logger.Nop(), staging, "/home/claude", "/workspace", projectRoot)
	if err == nil {
		t.Fatal("expected workspace-src rejection, got nil")
	}
	if !strings.Contains(err.Error(), "inside the project workspace") {
		t.Errorf("error = %q, want workspace-guard message", err)
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
