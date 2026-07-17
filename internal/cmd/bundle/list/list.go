// Package list provides the `clawker bundle list` command.
package list

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// ListOptions holds the options for the bundle list command.
type ListOptions struct {
	IOStreams     *iostreams.IOStreams
	TUI           *tui.TUI
	BundleManager func() (*bundle.Manager, error)

	Format *cmdutil.FormatFlags
}

// bundleRow is one row of `clawker bundle list` output: a bundle identity (or
// a never-fetched declared source) with its declaration↔cache state. It is the
// shape exposed to --json and --format templates.
type bundleRow struct {
	Bundle  string `json:"bundle"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Status  string `json:"status"`
	File    string `json:"file,omitempty"`
}

// NewCmdList creates the bundle list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:     f.IOStreams,
		TUI:           f.TUI,
		BundleManager: f.BundleManager,
		Format:        nil,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List bundles and their declaration↔cache state",
		Long: `Lists every bundle the current configuration knows about, one row per
identity: installed and in-place bundles that resolve, declared sources that
were never fetched, and cached bundles no live declaration matches.

The components a bundle ships are listed by the per-type inventory commands —
'clawker harness list', 'clawker stack list', and 'clawker monitor extensions'.`,
		Example: `  # List bundles
  clawker bundle list

  # Short form
  clawker bundle ls

  # Machine-readable output
  clawker bundle list --json`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)

	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}

	// Bundles() surfaces the bundle-load warnings; Statuses reuses its memoized
	// scan (and propagates the same C1 identity-collision error).
	_, warnings, err := mgr.Resolver().Bundles()
	if err != nil {
		return fmt.Errorf("resolving bundles: %w", err)
	}
	statuses, statusWarnings, err := mgr.Statuses()
	if err != nil {
		return fmt.Errorf("linking bundle declarations to the cache: %w", err)
	}
	warnings = append(warnings, statusWarnings...)

	if rErr := renderRows(opts, statuses); rErr != nil {
		return rErr
	}
	printWarnings(ios, warnings)
	printStatusHints(ios, statuses)
	return nil
}

// buildRow projects one status into a display row.
func buildRow(s bundle.Status) bundleRow {
	return bundleRow{
		Bundle:  statusIdentity(s),
		Version: orDash(s.Version),
		Source:  orDash(s.Source),
		Status:  statusText(s),
		File:    s.File,
	}
}

// renderRows writes the bundle rows in the format the flags select.
func renderRows(opts *ListOptions, statuses []bundle.Status) error {
	ios := opts.IOStreams
	rows := make([]bundleRow, 0, len(statuses))
	for _, s := range statuses {
		rows = append(rows, buildRow(s))
	}
	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, quietIdentity(r))
		}
		return nil
	case opts.Format.IsJSON():
		if err := cmdutil.WriteJSON(ios.Out, rows); err != nil {
			return fmt.Errorf("writing json: %w", err)
		}
		return nil
	case opts.Format.IsTemplate():
		if err := cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows)); err != nil {
			return fmt.Errorf("executing template: %w", err)
		}
		return nil
	default:
		return renderTable(opts, rows)
	}
}

// quietIdentity returns the one usable token for a --quiet row: the bundle
// identity, or the declared source for a never-fetched entry whose identity is
// not yet known.
func quietIdentity(r bundleRow) string {
	if r.Bundle != "-" {
		return r.Bundle
	}
	return r.Source
}

func renderTable(opts *ListOptions, rows []bundleRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(opts.IOStreams.ErrOut, "No bundles declared or cached.")
		return nil
	}
	table := opts.TUI.NewTable("BUNDLE", "VERSION", "SOURCE", "STATUS")
	for _, r := range rows {
		table.AddRow(r.Bundle, r.Version, r.Source, r.Status)
	}
	if err := table.Render(); err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}
	return nil
}

// printWarnings writes the bundle-load advisories to stderr.
func printWarnings(ios *iostreams.IOStreams, warnings []bundle.Warning) {
	cs := ios.ColorScheme()
	for _, w := range warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w.Message)
	}
}

// statusIdentity renders a status row's identity, or a dash for a declared
// source that was never fetched (its identity lives in the not-yet-cached
// manifest).
func statusIdentity(s bundle.Status) string {
	if s.ID.Namespace == "" && s.ID.Name == "" {
		return "-"
	}
	return s.ID.String()
}

// statusText renders a status row's state clause.
func statusText(s bundle.Status) string {
	switch s.State {
	case bundle.StatusResolving:
		if s.Tier == bundle.TierInPlace {
			return "in-place (" + s.File + ")"
		}
		return "installed (" + s.File + ")"
	case bundle.StatusNotInstalled:
		return "declared, not installed"
	case bundle.StatusUndeclared:
		return "cached, not declared"
	case bundle.StatusUnmanaged:
		return "cached, unmanaged (no source metadata)"
	default:
		return ""
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// printStatusHints writes the actionable declaration↔cache states to stderr in
// every output mode: a declared-but-uncached source (install it), a cached
// entry no declaration addresses (prune, or re-declare — hinted once per
// identity, since the identity may have several stranded values and may still
// be installed via another one, which is why the hint never suggests
// `bundle remove`: that purges the whole identity, serving entry included),
// and a hand-placed cache entry that can never resolve (purge).
func printStatusHints(ios *iostreams.IOStreams, statuses []bundle.Status) {
	cs := ios.ColorScheme()
	staleHinted := map[bundle.BundleID]bool{}
	for _, s := range statuses {
		switch s.State {
		case bundle.StatusNotInstalled:
			fmt.Fprintf(
				ios.ErrOut,
				"%s bundle source %s (declared in %s) is not installed — run `clawker bundle install`\n",
				cs.InfoIcon(),
				s.Source,
				s.File,
			)
		case bundle.StatusUndeclared:
			if staleHinted[s.ID] {
				continue
			}
			staleHinted[s.ID] = true
			fmt.Fprintf(
				ios.ErrOut,
				"%s bundle %s has cached content no declaration addresses — run `clawker bundle prune` to clear it, or re-declare the source to reactivate it\n",
				cs.WarningIcon(),
				s.ID,
			)
		case bundle.StatusUnmanaged:
			fmt.Fprintf(
				ios.ErrOut,
				"%s bundle %s is cached without source metadata and never resolves — `clawker bundle remove %s`\n",
				cs.WarningIcon(),
				s.ID,
				s.ID,
			)
		case bundle.StatusResolving:
		}
	}
}
