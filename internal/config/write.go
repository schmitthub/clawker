package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// WriteOptions controls how Write persists the current in-memory configuration.
//
// Path selects the target file:
//   - Empty: write to the currently loaded/configured Viper target.
//   - Non-empty: write to this explicit filesystem path.
//
// Safe controls overwrite behavior:
//   - false: create or overwrite (truncate) the target file.
//   - true: create only; return an error if the target already exists.
//
// Scope constrains persistence to a logical config file owner.
//   - Empty: selective dirty-root persistence to owning files (or explicit Path write).
//   - settings/project/registry: persist only dirty roots owned by that scope.
//
// Key optionally persists a single key.
//   - Empty: scope/default behavior applies.
//   - Non-empty: write only this key when dirty (scope inferred from ownership map when Scope is empty).
type WriteOptions struct {
	Path  string
	Safe  bool
	Scope ConfigScope
	Key   string
}

// Write persists the current in-memory configuration using WriteOptions.
//
// Behavior summary:
//   - Key set: persist only that dirty key (scope inferred/validated).
//   - Scope set: persist only dirty owned roots in that scope.
//   - Path empty: persist dirty roots to owning files across all scopes.
//   - Path set (without Key/Scope): write full merged config to explicit path.
//
// Access is protected by an RWMutex for safe concurrent writes.
func (c *configImpl) Write(opts WriteOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if opts.Key != "" {
		inferredScope, err := scopeFromNamespacedKey(opts.Key)
		if err != nil {
			return err
		}

		scope := inferredScope
		if opts.Scope != "" {
			if opts.Scope != inferredScope {
				return fmt.Errorf("key %q belongs to %q scope, not %q", opts.Key, inferredScope, opts.Scope)
			}
			scope = opts.Scope
		}

		if !c.isDirtyPath(opts.Key) {
			return nil
		}

		targetPath, err := c.resolveTargetPath(scope, opts.Path)
		if err != nil {
			return err
		}

		if !c.v.IsSet(opts.Key) {
			return &KeyNotFoundError{Key: opts.Key}
		}

		// Strip scope prefix for file-relative key.
		flatKey := stripScopePrefix(opts.Key)
		value := c.v.Get(opts.Key)
		if err := writeKeyToFile(targetPath, flatKey, value, opts.Safe); err != nil {
			return err
		}
		c.clearDirtyPath(opts.Key)
		return nil
	}

	if opts.Scope != "" {
		return c.writeDirtyRootsForScope(opts.Scope, opts.Path, opts.Safe)
	}

	if opts.Path == "" {
		scopes := []ConfigScope{ScopeSettings, ScopeRegistry, ScopeProject}
		for _, scope := range scopes {
			if err := c.writeDirtyRootsForScope(scope, "", opts.Safe); err != nil {
				return err
			}
		}
		return nil
	}

	// Write(Path) without Key/Scope: export all settings to explicit path.
	// Strip namespace prefixes so the output is a valid config file format,
	// merging children from each scope into a flat map.
	return withFileLock(opts.Path, func() error {
		if opts.Safe {
			if _, err := os.Stat(opts.Path); err == nil {
				return fmt.Errorf("config file already exists: %s", opts.Path)
			}
		}

		all := c.v.AllSettings()
		flat := make(map[string]any)
		for key, val := range all {
			if _, ok := validScopes[key]; ok {
				// This is a scope node — merge its children into the flat map.
				if scopeMap, mOk := val.(map[string]any); mOk {
					for k, v := range scopeMap {
						flat[k] = v
					}
				}
			} else {
				flat[key] = val
			}
		}

		encoded, err := yaml.Marshal(flat)
		if err != nil {
			return fmt.Errorf("encoding config %s: %w", opts.Path, err)
		}

		return atomicWriteFile(opts.Path, encoded, 0o644)
	})
}

func (c *configImpl) writeDirtyRootsForScope(scope ConfigScope, overridePath string, safe bool) error {
	dirtyRoots := c.dirtyOwnedRoots(scope)
	if len(dirtyRoots) == 0 {
		return nil
	}

	targetPath, err := c.resolveTargetPath(scope, overridePath)
	if err != nil {
		return err
	}

	// writeRootsToFile reads namespaced keys from Viper but writes flat keys to file.
	if err := writeRootsToFile(targetPath, dirtyRoots, scope, c.v, safe); err != nil {
		return err
	}

	for _, root := range dirtyRoots {
		// Clear dirty using the namespaced path: scope.root
		c.clearDirtyPath(string(scope) + "." + root)
	}
	return nil
}

func (c *configImpl) resolveTargetPath(scope ConfigScope, overridePath string) (string, error) {
	if overridePath != "" {
		return overridePath, nil
	}

	switch scope {
	case ScopeSettings:
		if c.settingsFile == "" {
			return "", fmt.Errorf("settings file path is not configured")
		}
		return c.settingsFile, nil
	case ScopeRegistry:
		if c.projectRegistryPath == "" {
			return "", fmt.Errorf("project registry path is not configured")
		}
		return c.projectRegistryPath, nil
	case ScopeProject:
		if c.projectConfigFile != "" {
			return c.projectConfigFile, nil
		}

		root, err := c.projectRootFromCurrentDir()
		if err == nil {
			return filepath.Join(root, clawkerProjectConfigFileName), nil
		}
		if errors.Is(err, ErrNotInProject) {
			if c.userProjectConfigFile == "" {
				return "", fmt.Errorf("project config path is not configured")
			}
			return c.userProjectConfigFile, nil
		}
		return "", err
	default:
		return "", fmt.Errorf("invalid write scope: %s", scope)
	}
}

// atomicWriteFile writes data to path using a temp-file + fsync + rename
// strategy so that a crash mid-write never leaves the target truncated or
// partial. The temp file is created in the target's parent directory to
// guarantee same-filesystem rename semantics on POSIX.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory for %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, ".clawker-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("setting permissions on temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("renaming temp file to %s: %w", path, err)
	}

	success = true
	return nil
}

// withFileLock acquires an advisory file lock on path+".lock" before running fn,
// providing cross-process mutual exclusion for config file writes.
func withFileLock(path string, fn func() error) error {
	fl := flock.New(path + ".lock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locked, err := fl.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring file lock for %s: %w", path, err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring file lock for %s", path)
	}
	defer func() { _ = fl.Unlock() }()

	return fn()
}

func writeKeyToFile(path, key string, value any, safe bool) error {
	return withFileLock(path, func() error {
		v, exists, err := openConfigForWrite(path)
		if err != nil {
			return err
		}

		if safe && exists {
			return fmt.Errorf("config file already exists: %s", path)
		}

		v.Set(key, value)

		encoded, err := yaml.Marshal(v.AllSettings())
		if err != nil {
			return fmt.Errorf("encoding config %s: %w", path, err)
		}

		return atomicWriteFile(path, encoded, 0o644)
	})
}

// writeRootsToFile persists dirty root keys to a config file.
// roots are flat file-level keys (e.g. "build", "logging").
// scope is used to read from the namespaced Viper store (e.g. "project.build").
func writeRootsToFile(path string, roots []string, scope ConfigScope, source *viper.Viper, safe bool) error {
	return withFileLock(path, func() error {
		_, exists, err := openConfigForWrite(path)
		if err != nil {
			return err
		}

		if safe && exists {
			return fmt.Errorf("config file already exists: %s", path)
		}

		content := map[string]any{}
		if exists {
			bytes, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading config %s: %w", path, err)
			}
			if len(bytes) > 0 {
				if err := yaml.Unmarshal(bytes, &content); err != nil {
					return fmt.Errorf("parsing config %s: %w", path, err)
				}
			}
		}

		scopePrefix := string(scope) + "."
		for _, root := range roots {
			nsKey := scopePrefix + root
			if source.IsSet(nsKey) {
				content[root] = source.Get(nsKey)
				continue
			}
			delete(content, root)
		}

		encoded, err := yaml.Marshal(content)
		if err != nil {
			return fmt.Errorf("encoding config %s: %w", path, err)
		}

		return atomicWriteFile(path, encoded, 0o644)
	})
}

func openConfigForWrite(path string) (*viper.Viper, bool, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(path)

	exists := true
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists = false
		} else {
			return nil, false, fmt.Errorf("failed to stat config %s: %w", path, err)
		}
	}

	if exists {
		if err := v.ReadInConfig(); err != nil {
			return nil, false, fmt.Errorf("loading config %s: %w", path, err)
		}
	}

	return v, exists, nil
}

func writeIfMissingLocked(path string, content []byte) error {
	return withFileLock(path, func() error {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to stat config %s: %w", path, err)
		}

		return atomicWriteFile(path, content, 0o644)
	})
}
