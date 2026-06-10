// Package shared holds domain logic used by multiple alias subcommands:
// alias name and expansion validation, and the shipped-default lookup.
package shared

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/config"
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
// of user settings, independent of any files on disk).
func DefaultAliases() (map[string]string, error) {
	cfg, err := config.NewBlankConfig()
	if err != nil {
		return nil, fmt.Errorf("loading default settings: %w", err)
	}
	return cfg.Settings().Aliases, nil
}

// ExportTarget resolves the project config file that alias export writes
// to: the highest-priority discovered project layer that is shared —
// i.e. not a local override variant and not the user-level project config
// in the clawker config directory.
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
		if strings.Contains(filepath.Base(path), ".local.") {
			continue // local override — never the sharing target
		}
		return path, nil
	}
	return "", fmt.Errorf("no shared project config found; run inside a clawker project (see 'clawker init')")
}

// OpenExportStore opens an isolated store on the export target file only —
// no defaults layer, no walk-up, no user-level merging. The composite
// project store pre-marks every defaults-provenance field dirty (the
// settings-bootstrap behavior), so writing through it would materialize all
// schema defaults into the project file; the isolated store writes only the
// alias entries.
func OpenExportStore(target string) (*storage.Store[config.Project], error) {
	store, err := storage.New[config.Project]("",
		storage.WithPaths(filepath.Dir(target)),
		storage.WithFilenames(filepath.Base(target)),
	)
	if err != nil {
		return nil, fmt.Errorf("opening project config %s: %w", target, err)
	}
	return store, nil
}
