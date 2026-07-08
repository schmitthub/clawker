package cmdutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
)

// Registry row source labels and the placeholder path shown for a definition
// built into the binary, which has no on-disk registry path.
const (
	RegistrySourceProject = "project"
	RegistrySourceBuilt   = "built"
	RegistryBuiltinPath   = "(built-in)"
)

// RegistryRow is one row of `clawker stack list` / `clawker harness list`
// output: a stack or harness available in the current project.
type RegistryRow struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Shadows string `json:"shadows"`
}

// MergeRegistryRows merges a project registry (name→path) with the shipped
// (built-in) names into a sorted row set. A project entry that reuses a shipped
// name shadows the built-in definition — its Shadows field is set to
// RegistrySourceBuilt; a shipped name with no project entry is a plain
// built-in row.
func MergeRegistryRows(shipped []string, registered map[string]string) []RegistryRow {
	shippedSet := make(map[string]bool, len(shipped))
	for _, n := range shipped {
		shippedSet[n] = true
	}

	names := make(map[string]bool, len(shippedSet)+len(registered))
	for n := range shippedSet {
		names[n] = true
	}
	// An empty path is not a registration — enforce that invariant centrally
	// here rather than trusting each caller to pre-filter.
	for n, p := range registered {
		if p != "" {
			names[n] = true
		}
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	rows := make([]RegistryRow, 0, len(sorted))
	for _, name := range sorted {
		rows = append(rows, registryRow(name, registered, shippedSet))
	}
	return rows
}

// registryRow builds one row: a project registration (optionally shadowing a
// shipped name) or a plain shipped row. An empty registered path is not a
// registration (mirrors the seed filter in MergeRegistryRows).
func registryRow(name string, registered map[string]string, shippedSet map[string]bool) RegistryRow {
	path, isProject := registered[name]
	if !isProject || path == "" {
		return RegistryRow{Name: name, Path: RegistryBuiltinPath, Source: RegistrySourceBuilt, Shadows: ""}
	}
	shadows := ""
	if shippedSet[name] {
		shadows = RegistrySourceBuilt
	}
	return RegistryRow{Name: name, Path: path, Source: RegistrySourceProject, Shadows: shadows}
}

// ResolvedRegistryPath is the two-form result of resolving a register
// command's path argument. Keeping the forms in a named struct (rather than a
// positional string pair) makes the abs-vs-stored contract structural — a
// caller cannot silently swap them.
type ResolvedRegistryPath struct {
	// Abs is the absolute, cleaned on-disk location — used to validate the
	// target directory.
	Abs string
	// Stored is the form written into clawker.yaml — project-root-relative when
	// the target lives inside the project (so the entry stays portable), else
	// absolute.
	Stored string
}

// ResolveRegistryPath resolves a user-supplied path argument for a stack or
// harness register command into its absolute (validation) and stored
// (persistence) forms. A relative input is joined onto cwd for Abs. Stored is
// relative to projectRoot when the target lives inside it, else absolute; when
// projectRoot is empty (not run inside a project) Stored is always absolute.
//
// The input must not use ~ home-dir or $VAR environment-variable expansion —
// registry paths are dumb relative/absolute paths by design, matching the
// load-time front-door check in internal/config (which likewise rejects the
// characters anywhere, not just in expansion position).
func ResolveRegistryPath(projectRoot, cwd, input string) (ResolvedRegistryPath, error) {
	if input == "" {
		return ResolvedRegistryPath{}, errors.New("path must not be empty")
	}
	if strings.Contains(input, "~") {
		return ResolvedRegistryPath{}, fmt.Errorf(
			"path %q must not use ~ home-dir expansion — pass a relative or absolute path", input)
	}
	if strings.Contains(input, "$") {
		return ResolvedRegistryPath{}, fmt.Errorf(
			"path %q must not use $VAR environment-variable expansion — pass a relative or absolute path", input)
	}

	abs := input
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, input)
	}
	abs = filepath.Clean(abs)

	stored := abs
	if projectRoot != "" {
		if rel, relErr := filepath.Rel(projectRoot, abs); relErr == nil && !isEscaping(rel) {
			stored = rel
		}
	}
	return ResolvedRegistryPath{Abs: abs, Stored: stored}, nil
}

// isEscaping reports whether a project-root-relative path climbs out of the
// root (a leading "..") — in which case the target is outside the project and
// must be stored as an absolute path instead.
func isEscaping(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PrimaryWritePath returns the absolute path of the config file a store's next
// write to a new key lands in (its highest-priority write target), or "" when
// it can't be determined. Register commands use it to show the user which
// config file a registration was written to — the destination is otherwise
// invisible (a project vs. user-level clawker.yaml is decided by the store's
// discovery, not by the command).
func PrimaryWritePath[T storage.Schema](store *storage.Store[T]) string {
	targets, err := store.WriteTargets()
	if err != nil || len(targets) == 0 {
		return ""
	}
	return targets[0].Path
}

// RenderRegistryRows writes registry rows in the format the flags select:
// -q emits names only, --json emits JSON, --format TEMPLATE runs a Go
// template, and the default renders a NAME/PATH/SOURCE/SHADOWS table.
// emptyMsg is printed to stderr when there are no rows in table mode. Shared
// by `clawker stack list` and `clawker harness list`.
func RenderRegistryRows(
	ios *iostreams.IOStreams,
	ui *tui.TUI,
	format *FormatFlags,
	rows []RegistryRow,
	emptyMsg string,
) error {
	switch {
	case format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Name)
		}
		return nil
	case format.IsJSON():
		return wrapRender("writing json", WriteJSON(ios.Out, rows))
	case format.IsTemplate():
		return wrapRender("executing template", ExecuteTemplate(ios.Out, format.Template(), ToAny(rows)))
	default:
		return renderRegistryTable(ios, ui, rows, emptyMsg)
	}
}

func renderRegistryTable(ios *iostreams.IOStreams, ui *tui.TUI, rows []RegistryRow, emptyMsg string) error {
	if len(rows) == 0 {
		fmt.Fprintln(ios.ErrOut, emptyMsg)
		return nil
	}
	table := ui.NewTable("NAME", "PATH", "SOURCE", "SHADOWS")
	for _, r := range rows {
		table.AddRow(r.Name, r.Path, r.Source, r.Shadows)
	}
	return wrapRender("rendering table", table.Render())
}

// wrapRender wraps a render error with context, passing nil through unchanged
// (so a successful render never becomes a spurious non-nil error).
func wrapRender(msg string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}
