// Package containerfs prepares host Claude Code configuration for container injection.
//
// This is a leaf package: it imports internal/keyring, internal/logger, and stdlib only.
// No docker imports allowed.
package containerfs

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/keyring"
	"github.com/schmitthub/clawker/internal/logger"
)

// ResolveHostConfigDir returns the claude config dir ($CLAUDE_CONFIG_DIR or ~/.claude/).
// Returns error if the directory doesn't exist.
func ResolveHostConfigDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return "", fmt.Errorf("CLAUDE_CONFIG_DIR is set to %s but path is invalid: %w", dir, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("CLAUDE_CONFIG_DIR is set to %s but path is not a directory", dir)
		}
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	claudeDir := filepath.Join(home, ".claude")
	if info, err := os.Stat(claudeDir); err == nil && info.IsDir() {
		return claudeDir, nil
	}

	return "", fmt.Errorf("claude config dir not found on host: checked $CLAUDE_CONFIG_DIR and ~/.claude/")
}

// PrepareClaudeConfig creates a staging directory with host claude config
// prepared for container injection. Caller must call cleanup() when done.
//
// Handles: settings.json enabledPlugins merge, agents/, skills/, commands/,
// plugins/ (excluding install-counts-cache.json), known_marketplaces.json path fixup, symlink resolution.
func PrepareClaudeConfig(hostConfigDir, containerHomeDir, containerWorkDir string) (stagingDir string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}

	cleanupFn := func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			logger.Debug().Err(err).Str("path", tmpDir).Msg("failed to remove staging dir")
		}
	}

	stagingClaudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(stagingClaudeDir, 0o755); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("create staging .claude dir: %w", err)
	}

	// settings.json — extract only enabledPlugins
	if err := stageSettings(hostConfigDir, stagingClaudeDir); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("stage settings.json: %w", err)
	}

	// Copy directories: agents/, skills/, commands/
	for _, dir := range []string{"agents", "skills", "commands"} {
		if err := stageDirectory(hostConfigDir, stagingClaudeDir, dir); err != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("stage %s: %w", dir, err)
		}
	}

	// plugins/ — copy with cache, rewrite JSON paths for container
	if err := stagePlugins(hostConfigDir, stagingClaudeDir, containerHomeDir, containerWorkDir); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("stage plugins: %w", err)
	}

	return tmpDir, cleanupFn, nil
}

// PrepareCredentials creates a staging directory with credentials.json.
// Sources: keyring first, then fallback to $CLAUDE_CONFIG_DIR/.credentials.json.
func PrepareCredentials(hostConfigDir string) (stagingDir string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "clawker-creds-*")
	if err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}

	cleanupFn := func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			logger.Debug().Err(err).Str("path", tmpDir).Msg("failed to remove staging dir")
		}
	}

	stagingClaudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(stagingClaudeDir, 0o755); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("create staging .claude dir: %w", err)
	}

	credsDst := filepath.Join(stagingClaudeDir, ".credentials.json")

	// Try keyring first.
	creds, keyringErr := keyring.GetClaudeCodeCredentials()
	if keyringErr == nil {
		data, err := json.Marshal(creds)
		if err != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("marshal keyring credentials: %w", err)
		}
		if err := os.WriteFile(credsDst, data, 0o600); err != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("write credentials: %w", err)
		}
		logger.Debug().Msg("credentials sourced from OS keyring")
		return tmpDir, cleanupFn, nil
	}

	logger.Debug().Err(keyringErr).Msg("keyring credentials not available, trying file fallback")

	// Fallback to file.
	filePath := filepath.Join(hostConfigDir, ".credentials.json")
	data, fileErr := os.ReadFile(filePath)
	if fileErr == nil && len(data) > 0 {
		if err := os.WriteFile(credsDst, data, 0o600); err != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("write credentials: %w", err)
		}
		logger.Debug().Str("path", filePath).Msg("credentials sourced from file fallback")
		return tmpDir, cleanupFn, nil
	}

	cleanupFn()
	return "", nil, fmt.Errorf(
		"no claude code credentials found: authenticate on the host first or set agent.claude_code.use_host_auth: false",
	)
}

// PrepareOnboardingTar creates a tar archive containing ~/.claude.json
// with {hasCompletedOnboarding: true} for CopyToContainer.
//
// The tar contains a single file named ".claude.json" — the caller specifies
// the container home directory as the extraction destination.
//
// TODO: containerHomeDir is currently unused — evaluate if needed for custom home paths.
func PrepareOnboardingTar(containerHomeDir string) (io.Reader, error) {
	content := []byte(`{"hasCompletedOnboarding":true}` + "\n")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name:    ".claude.json",
		Mode:    0o600,
		Size:    int64(len(content)),
		Uid:     config.ContainerUID,
		Gid:     config.ContainerGID,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("write tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return nil, fmt.Errorf("write tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	return &buf, nil
}

// PreparePostInitTar creates a tar archive containing a post-init script at .clawker/post-init.sh.
// The script is prefixed with a bash shebang and set -e, then the user's commands verbatim.
// The tar is designed for extraction at /home/claude, producing /home/claude/.clawker/post-init.sh.
// Returns an error if the script is empty or whitespace-only.
func PreparePostInitTar(script string) (io.Reader, error) {
	if strings.TrimSpace(script) == "" {
		return nil, fmt.Errorf("post-init script content is empty")
	}
	content := []byte("#!/bin/bash\nset -e\n" + script)
	now := time.Now()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Directory entry: .clawker/
	dirHdr := &tar.Header{
		Typeflag: tar.TypeDir,
		Name:     ".clawker/",
		Mode:     0o755,
		Uid:      config.ContainerUID,
		Gid:      config.ContainerGID,
		ModTime:  now,
	}
	if err := tw.WriteHeader(dirHdr); err != nil {
		return nil, fmt.Errorf("write dir header: %w", err)
	}

	// File entry: .clawker/post-init.sh
	fileHdr := &tar.Header{
		Name:    ".clawker/post-init.sh",
		Mode:    0o755,
		Size:    int64(len(content)),
		Uid:     config.ContainerUID,
		Gid:     config.ContainerGID,
		ModTime: now,
	}
	if err := tw.WriteHeader(fileHdr); err != nil {
		return nil, fmt.Errorf("write file header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return nil, fmt.Errorf("write file content: %w", err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	return &buf, nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// stageSettings reads settings.json from hostDir and writes only the
// enabledPlugins key to stagingDir/settings.json.
func stageSettings(hostDir, stagingDir string) error {
	src := filepath.Join(hostDir, "settings.json")

	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		logger.Debug().Str("path", src).Msg("settings.json not found, skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read settings.json: %w", err)
	}

	var full map[string]any
	if err := json.Unmarshal(data, &full); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}

	enabledPlugins, ok := full["enabledPlugins"]
	if !ok {
		logger.Debug().Msg("settings.json has no enabledPlugins key, skipping")
		return nil
	}

	filtered := map[string]any{
		"enabledPlugins": enabledPlugins,
	}

	out, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal filtered settings: %w", err)
	}

	dst := filepath.Join(stagingDir, "settings.json")
	return os.WriteFile(dst, out, 0o644)
}

// stageDirectory copies an entire directory from hostDir to stagingDir,
// resolving symlinks at the source level.
func stageDirectory(hostDir, stagingDir, name string) error {
	src := filepath.Join(hostDir, name)

	// Resolve symlinks on the source directory itself.
	resolved, err := filepath.EvalSymlinks(src)
	if os.IsNotExist(err) {
		logger.Debug().Str("dir", name).Msg("directory not found on host, skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve symlinks for %s: %w", name, err)
	}

	dst := filepath.Join(stagingDir, name)
	return copyDir(resolved, dst)
}

// stagePlugins copies the plugins/ directory (including cache/) and rewrites
// host-absolute paths in known_marketplaces.json and installed_plugins.json.
func stagePlugins(hostDir, stagingDir, containerHomeDir, containerWorkDir string) error {
	src := filepath.Join(hostDir, "plugins")

	resolved, err := filepath.EvalSymlinks(src)
	if os.IsNotExist(err) {
		logger.Debug().Msg("plugins/ directory not found on host, skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve symlinks for plugins: %w", err)
	}

	dst := filepath.Join(stagingDir, "plugins")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create plugins staging dir: %w", err)
	}

	err = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(resolved, path)
		if err != nil {
			return err
		}

		// Skip install-counts-cache.json at the top level.
		if rel == "install-counts-cache.json" {
			return nil
		}

		target := filepath.Join(dst, rel)

		// Handle symlinks: resolve and recurse if directory, copy if file.
		if d.Type()&os.ModeSymlink != 0 {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve symlink %s: %w", path, err)
			}
			info, err := os.Stat(realPath)
			if err != nil {
				return fmt.Errorf("stat symlink target %s: %w", realPath, err)
			}
			if info.IsDir() {
				return copyDir(realPath, target)
			}
			return copyFile(realPath, target)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
	if err != nil {
		return fmt.Errorf("walk plugins: %w", err)
	}

	// Build rewrite rules for host→container path translation.
	hostPluginsPrefix := filepath.Join(hostDir, "plugins")
	containerPluginsPrefix := filepath.Join(containerHomeDir, ".claude", "plugins")

	// Rewrite known_marketplaces.json if it exists.
	mpPath := filepath.Join(dst, "known_marketplaces.json")
	if _, statErr := os.Stat(mpPath); statErr == nil {
		if err := rewriteJSONFile(mpPath, []pathRewriteRule{
			{key: "installPath", hostPrefix: hostPluginsPrefix, containerPath: containerPluginsPrefix},
			{key: "installLocation", hostPrefix: hostPluginsPrefix, containerPath: containerPluginsPrefix},
		}); err != nil {
			return fmt.Errorf("rewrite known_marketplaces.json: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		logger.Debug().Err(statErr).Str("path", mpPath).Msg("plugin file stat failed")
	}

	// Rewrite installed_plugins.json if it exists.
	ipPath := filepath.Join(dst, "installed_plugins.json")
	if _, statErr := os.Stat(ipPath); statErr == nil {
		if err := rewriteJSONFile(ipPath, []pathRewriteRule{
			{key: "installPath", hostPrefix: hostPluginsPrefix, containerPath: containerPluginsPrefix},
			{key: "projectPath", containerPath: containerWorkDir},
		}); err != nil {
			return fmt.Errorf("rewrite installed_plugins.json: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		logger.Debug().Err(statErr).Str("path", ipPath).Msg("plugin file stat failed")
	}

	return nil
}

// rewriteJSONPaths recursively walks a JSON value and rewrites matching keys
// string values that start with hostPrefix with containerPrefix.
// pathRewriteRule describes a JSON key whose string value should be rewritten.
type pathRewriteRule struct {
	key           string // JSON key to match
	hostPrefix    string // non-empty: prefix swap; empty: replace entire value
	containerPath string // replacement prefix or full value
}

// rewriteJSONFile reads a JSON file, applies path rewrite rules, and writes it back.
func rewriteJSONFile(path string, rules []pathRewriteRule) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}

	rewriteJSONPaths(parsed, rules)

	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}

func rewriteJSONPaths(v any, rules []pathRewriteRule) {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			matched := false
			for _, rule := range rules {
				if k == rule.key {
					if s, ok := child.(string); ok {
						if rule.hostPrefix != "" {
							// Prefix swap: only when value has the prefix
							if strings.HasPrefix(s, rule.hostPrefix) {
								val[k] = rule.containerPath + s[len(rule.hostPrefix):]
							}
						} else {
							// Full replacement: replace entire value
							val[k] = rule.containerPath
						}
					}
					matched = true
					break
				}
			}
			if !matched {
				rewriteJSONPaths(child, rules)
			}
		}
	case []any:
		for _, item := range val {
			rewriteJSONPaths(item, rules)
		}
	}
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		// Handle symlinks: resolve and recurse if directory, copy if file.
		if d.Type()&os.ModeSymlink != 0 {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve symlink %s: %w", path, err)
			}
			info, err := os.Stat(realPath)
			if err != nil {
				return fmt.Errorf("stat symlink target %s: %w", realPath, err)
			}
			if info.IsDir() {
				return copyDir(realPath, target)
			}
			return copyFile(realPath, target)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single file preserving permissions.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}

	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	return os.WriteFile(dst, data, info.Mode())
}
