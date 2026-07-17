package cmdutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// InventoryOptions holds the options for a per-type component inventory
// command (`clawker stack list`, `clawker harness list`,
// `clawker monitor extensions`).
type InventoryOptions struct {
	IOStreams     *iostreams.IOStreams
	TUI           *tui.TUI
	BundleManager func() (*bundle.Manager, error)

	// Type is the component type the command inventories.
	Type bundle.ComponentType
	// Empty is the stderr notice when nothing of the type resolves.
	Empty string

	Format *FormatFlags
}

// InventorySpec is the per-command wording and component type for an
// inventory command built with NewInventoryListCommand.
type InventorySpec struct {
	Use     string
	Aliases []string
	Short   string
	Long    string
	Example string
	// Type is the component type the command inventories.
	Type bundle.ComponentType
	// Empty is the stderr notice when nothing of the type resolves.
	Empty string
}

// inventoryRow is one row of inventory output: a resolvable component with
// its provenance. It is the shape exposed to --json and --format templates.
type inventoryRow struct {
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Source   string   `json:"source"`
	Bundle   string   `json:"bundle,omitempty"`
	Shadowed bool     `json:"shadowed"`
	Shadows  []string `json:"shadows,omitempty"`
}

// NewInventoryListCommand builds a read-only inventory command for one
// component type: a NAME/VERSION/SOURCE table over the three resolution tiers,
// with '!' shadow markers and bundle-sourced rows naming their owning bundle.
func NewInventoryListCommand(
	f *Factory,
	runF func(context.Context, *InventoryOptions) error,
	spec InventorySpec,
) *cobra.Command {
	opts := &InventoryOptions{
		IOStreams:     f.IOStreams,
		TUI:           f.TUI,
		BundleManager: f.BundleManager,
		Type:          spec.Type,
		Empty:         spec.Empty,
		Format:        nil,
	}

	cmd := &cobra.Command{
		Use:     spec.Use,
		Aliases: spec.Aliases,
		Short:   spec.Short,
		Long:    spec.Long,
		Example: spec.Example,
		Args:    NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return inventoryListRun(cmd.Context(), opts)
		},
	}

	opts.Format = AddFormatFlags(cmd)

	return cmd
}

func inventoryListRun(_ context.Context, opts *InventoryOptions) error {
	ios := opts.IOStreams

	mgr, err := opts.BundleManager()
	if err != nil {
		return fmt.Errorf("loading bundle manager: %w", err)
	}
	items, warnings, err := mgr.Inventory(opts.Type)
	if err != nil {
		return fmt.Errorf("listing %s components: %w", opts.Type, err)
	}

	rows := make([]inventoryRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, buildInventoryRow(item))
	}
	if rErr := renderInventoryRows(opts, rows); rErr != nil {
		return rErr
	}

	cs := ios.ColorScheme()
	for _, w := range warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w.Message)
	}
	return nil
}

// buildInventoryRow projects one inventory item into a display row.
func buildInventoryRow(item bundle.InventoryItem) inventoryRow {
	row := inventoryRow{
		Name:     item.Name,
		Version:  item.Version,
		Source:   item.Provenance.Source(),
		Bundle:   "",
		Shadowed: item.Provenance.Shadowed(),
		Shadows:  nil,
	}
	if item.Bundle != (bundle.BundleID{Namespace: "", Name: ""}) {
		row.Bundle = item.Bundle.String()
	}
	for _, s := range item.Provenance.Shadows {
		row.Shadows = append(row.Shadows, s.Source())
	}
	return row
}

// renderInventoryRows writes the rows in the format the flags select.
func renderInventoryRows(opts *InventoryOptions, rows []inventoryRow) error {
	ios := opts.IOStreams
	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Name)
		}
		return nil
	case opts.Format.IsJSON():
		if err := WriteJSON(ios.Out, rows); err != nil {
			return fmt.Errorf("writing json: %w", err)
		}
		return nil
	case opts.Format.IsTemplate():
		if err := ExecuteTemplate(ios.Out, opts.Format.Template(), ToAny(rows)); err != nil {
			return fmt.Errorf("executing template: %w", err)
		}
		return nil
	default:
		return renderInventoryTable(opts, rows)
	}
}

func renderInventoryTable(opts *InventoryOptions, rows []inventoryRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(opts.IOStreams.ErrOut, opts.Empty)
		return nil
	}
	table := opts.TUI.NewTable("NAME", "VERSION", "SOURCE")
	for _, r := range rows {
		table.AddRow(r.Name, orDash(r.Version), sourceCell(r))
	}
	if err := table.Render(); err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}
	return nil
}

// sourceCell renders a row's SOURCE cell: the winning source, plus the
// '!'-marked shadowed sources when the row shadows a farther tier.
func sourceCell(r inventoryRow) string {
	if !r.Shadowed {
		return r.Source
	}
	return r.Source + "  ! shadows " + strings.Join(r.Shadows, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
