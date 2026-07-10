// Package list provides the `clawker bundle list` command.
package list

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
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

// componentRow is one row of `clawker bundle list` output: a resolvable
// component of any type, with its resolution provenance. It is the shape
// exposed to --json and --format templates.
type componentRow struct {
	Address    string `json:"address"`
	Type       string `json:"type"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"`
	Shadowed   bool   `json:"shadowed"`
}

// allComponentTypes is the fixed enumeration order for the merged listing —
// harnesses, then stacks, then monitoring extensions.
func allComponentTypes() []bundle.ComponentType {
	return []bundle.ComponentType{
		bundle.ComponentHarness,
		bundle.ComponentStack,
		bundle.ComponentMonitoring,
	}
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
		Short:   "List resolvable components and their provenance",
		Long: `Lists every resolvable component — harnesses, stacks, and monitoring
extensions — across all three tiers: the embedded floor, loose convention
directories, and installed bundles.

Each row shows the component address (bare for floor/loose, qualified
namespace.bundle.component for a bundle), its type, the owning bundle version
where applicable, the resolution source, and — for a component that shadows a
farther tier — the shadowed sources marked with '!'.`,
		Example: `  # List all components
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
	resolver := mgr.Resolver()

	// Bundles() surfaces the C1 identity-collision error and the bundle-load
	// warnings once; List memoizes over the same scan, so version lookups and
	// per-type listing below reuse it.
	bundles, warnings, err := resolver.Bundles()
	if err != nil {
		return fmt.Errorf("resolving bundles: %w", err)
	}

	var rows []componentRow
	for _, t := range allComponentTypes() {
		components, _, listErr := resolver.List(t)
		if listErr != nil {
			return fmt.Errorf("listing %s components: %w", t, listErr)
		}
		for _, c := range components {
			rows = append(rows, buildRow(c, bundles))
		}
	}

	if rErr := renderRows(opts, rows); rErr != nil {
		return rErr
	}
	printWarnings(ios, warnings)
	printDeclaredSourceHint(ios, mgr.Declarations())
	return nil
}

// buildRow projects one resolved component into a display row, looking up the
// owning bundle's manifest version for a qualified component.
func buildRow(c bundle.Component, bundles map[bundle.BundleID]*bundle.ResolvedBundle) componentRow {
	provenance := ""
	if c.Provenance.Shadowed() {
		provenance = "! " + shadowClause(c.Provenance)
	}
	return componentRow{
		Address:    c.Address.String(),
		Type:       c.Type.String(),
		Version:    componentVersion(c, bundles),
		Source:     c.Provenance.Source(),
		Provenance: provenance,
		Shadowed:   c.Provenance.Shadowed(),
	}
}

// shadowClause renders the "shadows a, b" clause listing the farther-tier
// sources a component shadowed.
func shadowClause(p bundle.Provenance) string {
	sources := make([]string, 0, len(p.Shadows))
	for _, s := range p.Shadows {
		sources = append(sources, s.Source())
	}
	return "shadows " + strings.Join(sources, ", ")
}

// componentVersion returns the owning bundle's manifest version for a qualified
// component, or "-" for a bare component or a bundle without a declared version
// (the source-sha fallback is a fetch-phase concern, not yet available).
func componentVersion(c bundle.Component, bundles map[bundle.BundleID]*bundle.ResolvedBundle) string {
	if !c.Address.Qualified() {
		return "-"
	}
	if rb, ok := bundles[c.Provenance.Bundle]; ok && rb.Bundle.Manifest.Version != "" {
		return rb.Bundle.Manifest.Version
	}
	return "-"
}

// renderRows writes the component rows in the format the flags select.
func renderRows(opts *ListOptions, rows []componentRow) error {
	ios := opts.IOStreams
	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Address)
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

func renderTable(opts *ListOptions, rows []componentRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(opts.IOStreams.ErrOut, "No components resolvable.")
		return nil
	}
	table := opts.TUI.NewTable("ADDRESS", "TYPE", "VERSION", "SOURCE", "PROVENANCE")
	for _, r := range rows {
		table.AddRow(r.Address, r.Type, r.Version, r.Source, r.Provenance)
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

// printDeclaredSourceHint notes declared remote bundle sources whose cache
// status this listing cannot yet confirm — linking a remote declaration to its
// cache entry is a fetch-phase concern. Local (in-place) sources always resolve
// into the listing above, so only remote declarations warrant the hint.
func printDeclaredSourceHint(ios *iostreams.IOStreams, decls []config.BundleDeclaration) {
	remote := 0
	for _, d := range decls {
		if d.Source.URL != "" {
			remote++
		}
	}
	if remote == 0 {
		return
	}
	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut,
		"%s %d remote bundle source(s) declared — run `clawker bundle install` to fetch any not yet cached.\n",
		cs.InfoIcon(), remote)
}
