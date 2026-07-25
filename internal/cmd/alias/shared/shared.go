// Package shared holds domain logic used by multiple alias subcommands:
// alias name and expansion validation, and the shipped-default lookup.
package shared

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/shlex"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// keyAliases is the project-config key holding the alias map. Alias entries
// are dynamic map keys under it, addressed as the segment pair
// {keyAliases, <name>} — never as a dotted string.
const keyAliases = "aliases"

// ValidCommandFunc reports whether name belongs to a real (non-alias)
// clawker command or one of its cobra aliases. The root command provides
// the implementation once the full command tree is built.
type ValidCommandFunc func(name string) bool

// ValidateName checks that an alias name is usable as a single command token.
func ValidateName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("alias name must not be empty")
	case len(strings.Fields(name)) != 1 || name != strings.TrimSpace(name):
		return fmt.Errorf("alias name %q must be a single word", name)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("alias name %q must not start with %q", name, "-")
	}
	return nil
}

// SplitExpansion validates an alias expansion and returns its argv tokens.
func SplitExpansion(expansion string) ([]string, error) {
	if strings.TrimSpace(expansion) == "" {
		return nil, fmt.Errorf("alias expansion must not be empty")
	}
	tokens, err := shlex.Split(expansion)
	if err != nil {
		return nil, fmt.Errorf("invalid alias expansion %q: %w", expansion, err)
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("alias expansion must not be empty")
	}
	return tokens, nil
}

// ValidateExpansionTarget checks that an expansion's first token resolves to
// something executable: a real clawker command or another configured alias.
// name is the alias being defined — a direct self-reference is rejected.
func ValidateExpansionTarget(name, expansion string, validCommand ValidCommandFunc, aliases map[string]string) error {
	tokens, err := SplitExpansion(expansion)
	if err != nil {
		return err
	}
	first := tokens[0]
	if first == name {
		return fmt.Errorf("alias %q must not reference itself", name)
	}
	if validCommand != nil && validCommand(first) {
		return nil
	}
	if _, ok := aliases[first]; ok {
		return nil
	}
	return fmt.Errorf("invalid alias expansion: %q is not a clawker command or configured alias", first)
}

// DefaultAliases returns the shipped default alias map (the defaults layer
// of the project config, independent of any files on disk).
func DefaultAliases() (map[string]string, error) {
	cfg, err := config.NewBlankConfig()
	if err != nil {
		return nil, fmt.Errorf("loading default config: %w", err)
	}
	return cfg.Aliases(), nil
}

// SetTarget resolves the file that alias set writes to: the user-level
// project config in the clawker config directory (the base config file
// layer). The file is created on first write if missing.
func SetTarget() (string, error) {
	return consts.UserProjectConfigFilePath()
}

// ExportTarget resolves the project config file that alias export writes
// to: the most local, highest-priority discovered project layer in the
// walk-up. Export never creates files — only already-discovered layers
// qualify — and the user-level project config in the clawker config
// directory is not a walk-up layer.
func ExportTarget(cfg config.Config) (string, error) {
	configDir := filepath.Clean(config.ConfigDir())
	for _, layer := range cfg.ProjectStore().Layers() {
		if layer.Path == "" {
			continue // defaults / string-backed layer
		}
		path := filepath.Clean(layer.Path)
		if filepath.Dir(path) == configDir {
			continue // user-level project config, not the project's
		}
		return path, nil
	}
	return "", fmt.Errorf("no project config found in the walk-up; run inside a clawker project (see 'clawker init')")
}

// AliasKey returns the store key segments addressing one alias entry —
// {"aliases", name}. Segments, never a dotted string: a name containing a
// literal dot addresses the map entry exactly instead of reparsing as nesting.
func AliasKey(name string) []string {
	return []string{keyAliases, name}
}

// SamePath reports whether a and b denote the same file after cleaning.
func SamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// WriteAliasEntries stages each name→expansion entry in the project store and
// flushes each one to path, then reports the write on out. Every other staged
// field stays staged: WriteFieldTo persists exactly the key it names, merged
// into the file's current contents, so an alias command touches nothing but
// its own alias entries.
func WriteAliasEntries(out io.Writer, cfg config.Config, path string, entries map[string]string) error {
	if len(entries) == 0 {
		return nil // nothing staged, nothing written, nothing to report
	}
	store := cfg.ProjectStore()
	for name, expansion := range entries {
		key := AliasKey(name)
		if err := store.Set(key, expansion); err != nil {
			return fmt.Errorf("setting alias %q: %w", name, err)
		}
		if err := store.WriteFieldTo(path, key...); err != nil {
			return fmt.Errorf("saving %s: %w", path, err)
		}
	}
	fmt.Fprintf(out, "Wrote %s\n", path)
	return nil
}

// DeleteAliasEntry stages the removal of the alias from the merged view and
// flushes the deletion to path, then reports the write on out.
//
// Removing from the merged view is what stages the deletion, so the caller
// walks the file layers highest priority first: each flush drops the entry
// from one file, and the remerge that follows re-exposes the next layer's
// value for the next removal. A name the merged view no longer serves a live
// value for — every remaining layer entry is a bare `name:` (unset) — stages
// nothing and writes nothing; there is no live alias left to delete.
func DeleteAliasEntry(out io.Writer, cfg config.Config, path, name string) error {
	store := cfg.ProjectStore()
	key := AliasKey(name)
	if err := store.Remove(key...); err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil
		}
		return fmt.Errorf("removing alias %q: %w", name, err)
	}
	if err := store.WriteFieldTo(path, key...); err != nil {
		return fmt.Errorf("saving %s: %w", path, err)
	}
	fmt.Fprintf(out, "Wrote %s\n", path)
	return nil
}

// LayersContaining returns the absolute paths of every discovered file
// layer whose raw data carries an entry for the alias, ordered highest
// priority first. Defaults (virtual) layers are excluded.
func LayersContaining(cfg config.Config, name string) []string {
	var paths []string
	for _, layer := range cfg.ProjectStore().Layers() {
		if layer.Path == "" {
			continue // defaults / string-backed layer
		}
		aliases, ok := layer.Data[keyAliases].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := aliases[name]; ok {
			paths = append(paths, layer.Path)
		}
	}
	return paths
}
