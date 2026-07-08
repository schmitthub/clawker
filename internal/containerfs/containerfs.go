// Package containerfs prepares host harness configuration for container
// injection, driven by the selected harness bundle's staging manifest
// (config.Staging) — explicit host→container copy directives (glob-capable
// src, optional JSON key allowlist, per-file skips, JSON path rewrites
// host→container). Only host state OUTSIDE the workspace is staged; the
// workspace arrives via mount. Credentials are never copied from the host —
// the user authenticates in the container and the token family persists in
// the config volume.
//
// This is a leaf package: it imports internal/config, internal/logger, and
// stdlib only. No docker imports allowed.
package containerfs

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// ResolveHostMountSource expands a manifest mount entry's host src and
// returns it when the directory exists on the host. The bool result is
// false when the dir does not exist — callers should skip the bind mount in
// that case rather than treating it as an error. Uses [os.Stat], so a
// symlink at the path resolves to its target. Never creates the directory;
// that is the harness's responsibility.
func ResolveHostMountSource(src string) (string, bool, error) {
	dir, err := config.ExpandHostPath(src)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("stat %s: %w", dir, err)
	case !info.IsDir():
		return "", false, fmt.Errorf("%s exists but is not a directory (mode=%s)", dir, info.Mode())
	}
	return dir, true, nil
}

// PrepareConfig creates a staging directory with host harness state
// prepared for container injection, following the manifest's staging spec:
// explicit copy directives (optionally reduced to a JSON key allowlist,
// with per-file skips + JSON path rewrites). Caller must call cleanup()
// when done. The staged layout mirrors the container home: each directive
// lands at <stagingDir>/<dest>.
func PrepareConfig(
	log *logger.Logger,
	staging config.Staging,
	containerHomeDir, containerWorkDir, hostProjectRoot string,
) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "clawker-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}

	cleanupFn := func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			log.Debug().Err(rmErr).Str("path", tmpDir).Msg("failed to remove staging dir")
		}
	}

	for _, c := range staging.Copy {
		if stageErr := stageCopy(log, c, tmpDir, containerHomeDir, containerWorkDir, hostProjectRoot); stageErr != nil {
			cleanupFn()
			return "", nil, fmt.Errorf("stage %s: %w", c.Src, stageErr)
		}
	}

	return tmpDir, cleanupFn, nil
}

// stageCopy executes one explicit copy directive: expand the host-side src
// (tokens, ~, env, glob), expand the container-side dest, and copy every
// match into the staging mirror. Missing sources skip; sources inside the
// project workspace are rejected — the workspace is mounted, never staged.
func stageCopy(
	log *logger.Logger,
	c config.CopySpec,
	tmpRoot, containerHomeDir, containerWorkDir, hostProjectRoot string,
) error {
	pattern, err := config.ExpandHostPath(c.Src)
	if err != nil {
		return fmt.Errorf("expand src %q: %w", c.Src, err)
	}

	matches, globbed, err := expandCopyMatches(pattern)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		log.Debug().Str("src", pattern).Msg("staging source not found on host, skipping")
		return nil
	}

	// A glob, a multi-match, or a trailing-slash dest lands each match
	// UNDER dest; a single literal src copies TO dest exactly.
	destRel := config.NormalizeContainerPath(c.Dest)
	destIsDir := globbed || len(matches) > 1 || strings.HasSuffix(c.Dest, "/")

	for _, match := range matches {
		if guardErr := guardWorkspaceSrc(match, hostProjectRoot); guardErr != nil {
			return guardErr
		}

		dst := filepath.Join(tmpRoot, filepath.FromSlash(destRel))
		containerDest := containerHomeDir + "/" + destRel
		if destIsDir {
			dst = filepath.Join(dst, filepath.Base(match))
			containerDest += "/" + filepath.Base(match)
		}

		if copyErr := stageMatch(log, c, match, dst, containerDest, containerWorkDir); copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// expandCopyMatches resolves a directive's expanded src pattern to concrete
// host paths: glob patterns fan out, literal paths stat (missing = no
// matches, a soft skip for the caller).
func expandCopyMatches(pattern string) ([]string, bool, error) {
	if config.HasGlobMeta(pattern) {
		matches, err := doublestar.FilepathGlob(pattern)
		if err != nil {
			return nil, true, fmt.Errorf("glob %s: %w", pattern, err)
		}
		return matches, true, nil
	}
	if _, err := os.Stat(pattern); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat %s: %w", pattern, err)
	}
	return []string{pattern}, false, nil
}

// stageMatch copies one matched host path into the staging mirror,
// dispatching on file-vs-directory.
func stageMatch(
	log *logger.Logger,
	c config.CopySpec,
	match, dst, containerDest, containerWorkDir string,
) error {
	resolved, err := filepath.EvalSymlinks(match)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Debug().Str("src", match).Msg("staging source vanished, skipping")
			return nil
		}
		return fmt.Errorf("resolve symlinks for %s: %w", match, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat %s: %w", resolved, err)
	}

	if info.IsDir() {
		return stageCopyDir(log, c, match, resolved, dst, containerDest, containerWorkDir)
	}
	return stageCopyFile(log, c, resolved, dst)
}

// guardWorkspaceSrc rejects staging sources inside the project workspace.
func guardWorkspaceSrc(src, hostProjectRoot string) error {
	if hostProjectRoot == "" {
		return nil
	}
	rel, err := filepath.Rel(hostProjectRoot, src)
	if err != nil {
		// Unrelatable paths (e.g. different volumes) are by definition
		// outside the workspace.
		return nil //nolint:nilerr // unrelatable = outside the workspace = allowed
	}
	if rel != "." && strings.HasPrefix(rel, "..") {
		return nil
	}
	return fmt.Errorf(
		"staging src %s is inside the project workspace %s — workspace content is mounted into the container, never staged",
		src,
		hostProjectRoot,
	)
}

// stageCopyFile copies a single matched file, applying the directive's
// optional JSON key allowlist.
func stageCopyFile(log *logger.Logger, c config.CopySpec, src, dst string) error {
	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o750); mkErr != nil {
		return fmt.Errorf("create staging dir for %s: %w", dst, mkErr)
	}

	if len(c.JSONKeys) == 0 {
		return copyFile(src, dst)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	out, keep, err := filterJSONKeys(data, c.JSONKeys, filepath.Base(src))
	if err != nil {
		return err
	}
	if !keep {
		log.Debug().Str("file", src).Msg("no allowlisted keys present, skipping")
		return nil
	}
	if writeErr := os.WriteFile(dst, out, 0o600); writeErr != nil {
		return fmt.Errorf("write filtered %s: %w", dst, writeErr)
	}
	return nil
}

// stageCopyDir copies a matched directory recursively, honoring the
// directive's skip list (paths relative to the directory root) and applying
// its JSON path rewrites (host paths → container paths).
func stageCopyDir(
	log *logger.Logger,
	c config.CopySpec,
	match, resolved, dst, containerDest, containerWorkDir string,
) error {
	if mkErr := os.MkdirAll(dst, 0o750); mkErr != nil {
		return fmt.Errorf("create staging dir %s: %w", dst, mkErr)
	}

	if copyErr := copyTreeContents(log, c.Skip, resolved, dst); copyErr != nil {
		return copyErr
	}

	rulesByFile, err := copyRewriteRules(c, match, containerDest, containerWorkDir)
	if err != nil {
		return err
	}
	return applyTreeRewrites(log, dst, rulesByFile)
}

// PrepareHookTar tars a shell-wrapped user hook script to .clawker/<name>.sh
// (mode 0755) for extraction at /home/claude. An empty script yields a valid
// no-op wrapper ("#!/bin/$shell\nset -e\n"), letting callers deliver a
// guaranteed-present script even when the hook is unset (overwriting any stale
// prior content).
func PrepareHookTar(cfg config.Config, shell, script, name string) (io.Reader, error) {
	if shell == "" {
		shell = "zsh"
	}
	content := []byte("#!/bin/" + shell + "\nset -e\n" + script)
	now := time.Now()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Directory entry: the in-container DotClawkerDir
	dirHdr := &tar.Header{
		Typeflag: tar.TypeDir,
		Name:     consts.DotClawkerDir + "/",
		Mode:     0o755,
		Uid:      cfg.ContainerUID(),
		Gid:      cfg.ContainerGID(),
		ModTime:  now,
	}
	if err := tw.WriteHeader(dirHdr); err != nil {
		return nil, fmt.Errorf("write dir header: %w", err)
	}

	// File entry: <name>.sh under DotClawkerDir
	fileHdr := &tar.Header{
		Name:    consts.DotClawkerDir + "/" + name + ".sh",
		Mode:    0o755,
		Size:    int64(len(content)),
		Uid:     cfg.ContainerUID(),
		Gid:     cfg.ContainerGID(),
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

// stageFile copies one manifest file entry from hostDir into stagingDir.
// With a json_keys allowlist, only the listed top-level keys are carried
// over (host config files can hold secrets and host-specific junk — the
// allowlist is deliberate); a file whose allowlisted keys are all absent is
// skipped entirely. Missing source files are skipped.

// filterJSONKeys reduces a JSON document to the staged file's key allowlist.
// The bool result is false when none of the allowlisted keys are present —
// the file should not be staged at all.
func filterJSONKeys(data []byte, keys []string, name string) ([]byte, bool, error) {
	var full map[string]any
	if err := json.Unmarshal(data, &full); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", name, err)
	}

	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		if v, ok := full[key]; ok {
			filtered[key] = v
		}
	}
	if len(filtered) == 0 {
		return nil, false, nil
	}

	out, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal filtered %s: %w", name, err)
	}
	return out, true, nil
}

// stageDirectory copies an entire directory from hostDir to stagingDir,
// resolving symlinks at the source level.

// stageTree copies a manifest tree entry recursively and applies its JSON
// path rewrites (host paths → container paths). Skip entries match paths
// relative to the tree root.

// copyTreeContents walks the resolved tree and copies every entry not on
// the skip list (matched relative to the tree root).
func copyTreeContents(log *logger.Logger, skip []string, resolved, dst string) error {
	err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, relErr := filepath.Rel(resolved, path)
		if relErr != nil {
			return fmt.Errorf("rel %s: %w", path, relErr)
		}

		if slices.Contains(skip, rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return copyEntry(log, path, d, filepath.Join(dst, rel))
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", resolved, err)
	}
	return nil
}

// treeRewriteRules groups the tree's manifest rewrites into per-file
// pathRewriteRule sets.
func copyRewriteRules(
	c config.CopySpec,
	hostTreePrefix, containerTreePrefix, containerWorkDir string,
) (map[string][]pathRewriteRule, error) {
	rulesByFile := make(map[string][]pathRewriteRule)
	for _, rw := range c.JSONRewrites {
		switch rw.Rewrite {
		case config.RewritePrefixSwap:
			rulesByFile[rw.File] = append(rulesByFile[rw.File],
				pathRewriteRule{key: rw.Key, hostPrefix: hostTreePrefix, containerPath: containerTreePrefix})
		case config.RewriteReplaceWithWorkdir:
			rulesByFile[rw.File] = append(rulesByFile[rw.File],
				pathRewriteRule{key: rw.Key, hostPrefix: "", containerPath: containerWorkDir})
		default:
			// harness.Load validates the vocabulary; reaching here means a
			// missed engine primitive, not user error.
			return nil, fmt.Errorf("json rewrite %q on %s: unknown rewrite kind", rw.Rewrite, rw.File)
		}
	}
	return rulesByFile, nil
}

// applyTreeRewrites runs each per-file rule set against the staged copy,
// skipping files the host tree does not carry.
func applyTreeRewrites(log *logger.Logger, dst string, rulesByFile map[string][]pathRewriteRule) error {
	for file, rules := range rulesByFile {
		path := filepath.Join(dst, file)
		if _, statErr := os.Stat(path); statErr != nil {
			if !os.IsNotExist(statErr) {
				log.Debug().Err(statErr).Str("path", path).Msg("rewrite target stat failed")
			}
			continue
		}
		if err := rewriteJSONFile(path, rules); err != nil {
			return fmt.Errorf("rewrite %s: %w", file, err)
		}
	}
	return nil
}

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

// copyEntry copies a single walked entry to target: symlinks are resolved
// and recursed if directories or copied if files, with broken symlinks
// skipped under a warning — Claude Code leaves dangling cache symlinks
// behind after plugin updates and ignores them itself.
func copyEntry(log *logger.Logger, path string, d fs.DirEntry, target string) error {
	if d.Type()&os.ModeSymlink != 0 {
		realPath, err := filepath.EvalSymlinks(path)
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("skipping broken symlink")
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", path, err)
		}
		info, err := os.Stat(realPath)
		if err != nil {
			return fmt.Errorf("stat symlink target %s: %w", realPath, err)
		}
		if info.IsDir() {
			return copyDir(log, realPath, target)
		}
		return copyFile(realPath, target)
	}

	if d.IsDir() {
		return os.MkdirAll(target, 0o755)
	}

	return copyFile(path, target)
}

// copyDir recursively copies a directory tree, resolving symlinks and
// skipping broken ones with a warning.
func copyDir(log *logger.Logger, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		return copyEntry(log, path, d, filepath.Join(dst, rel))
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
