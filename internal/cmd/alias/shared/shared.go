// Package shared holds domain logic used by multiple alias subcommands:
// alias name and expansion validation, and the shipped-default lookup.
package shared

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

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
	case strings.Contains(name, "."):
		// The store addresses alias entries by the dotted field path
		// "aliases.<name>"; a dot in the name reparses as nesting — set
		// writes the wrong nested shape (corrupting the file) and delete
		// silently no-ops on disk.
		return fmt.Errorf("alias name %q must not contain %q", name, ".")
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
	return cfg.Project().Aliases, nil
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

// OpenFileStore opens an isolated store on a single project config file —
// no defaults layer, no walk-up, no user-level merging. This scopes a write
// to exactly the alias entries: the composite project store marks every
// defaults-provenance field dirty at construction (how init/bootstrap
// materializes defaults), so a write through it would also backfill any
// schema fields the file doesn't carry — fine for init, surprising as a side
// effect of an alias command.
func OpenFileStore(target string) (*storage.Store[config.Project], error) {
	store, err := storage.New[config.Project]("",
		storage.WithPaths(filepath.Dir(target)),
		storage.WithFilenames(filepath.Base(target)),
	)
	if err != nil {
		return nil, fmt.Errorf("opening project config %s: %w", target, err)
	}
	return store, nil
}

// AliasFieldPath returns the dotted store path for one alias entry,
// e.g. "aliases.go" — the key used in provenance and layer lookups.
func AliasFieldPath(name string) string {
	return "aliases." + name
}

// SamePath reports whether a and b denote the same file after cleaning.
func SamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// WriteAliases applies mutate to the isolated store on path (see
// OpenFileStore), persists it, and reports the write on out. mutate
// receives a non-nil aliases map.
func WriteAliases(out io.Writer, path string, mutate func(map[string]string)) error {
	store, err := OpenFileStore(path)
	if err != nil {
		return err
	}
	if err := store.Set(func(p *config.Project) {
		if p.Aliases == nil {
			p.Aliases = make(map[string]string)
		}
		mutate(p.Aliases)
	}); err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}
	if err := store.WriteTo(path); err != nil {
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
		aliases, ok := layer.Data["aliases"].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := aliases[name]; ok {
			paths = append(paths, layer.Path)
		}
	}
	return paths
}
