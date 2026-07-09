package units

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
	"github.com/schmitthub/clawker/internal/tui"
)

// ListOptions holds the inputs for `clawker monitor list`.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Config    func() (config.Config, error)
	Format    *cmdutil.FormatFlags
}

// unitRow is one row of `clawker monitor list` output.
type unitRow struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source"`
	Active string `json:"active"`
}

// List output vocabulary.
const (
	activeYes          = "yes"
	activeNo           = "-"
	sourceDiscoverable = "discoverable"
	pathMissingSuffix  = " (missing)"
)

// NewCmdList creates the `clawker monitor list` command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Config:    f.Config,
		Format:    nil, // set below via AddFormatFlags
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List monitoring units",
		Long: `Lists every monitoring unit on this host: built-in units shipped inside
embedded harness bundles, registered units from the host-global registry
(settings.yaml), and — when run inside a project — units shipped by the
project's registered harness bundles that are not yet registered
(discoverable; promote one with 'clawker monitor register <path>').

Only ACTIVE units are seeded into the monitoring stack.`,
		Example: `  # List units
  clawker monitor list

  # Names only
  clawker monitor list -q

  # JSON output
  clawker monitor list --json`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	cmd.Flags().Lookup("quiet").Usage = "Only display unit names"

	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	units, err := monitor.ResolveUnits(cfg)
	if err != nil {
		return fmt.Errorf("resolving monitoring units: %w", err)
	}

	rows := make([]unitRow, 0, len(units))
	for _, u := range units {
		rows = append(rows, resolvedRow(u))
	}
	for _, d := range monitor.DiscoverableUnits(cfg) {
		rows = append(rows, unitRow{
			Name:   d.Name,
			Path:   d.Path,
			Source: sourceDiscoverable + " via harness " + d.Harness,
			Active: activeNo,
		})
	}

	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Name)
		}
		return nil
	case opts.Format.IsJSON():
		if jsonErr := cmdutil.WriteJSON(ios.Out, rows); jsonErr != nil {
			return fmt.Errorf("writing json: %w", jsonErr)
		}
		return nil
	case opts.Format.IsTemplate():
		if tmplErr := cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows)); tmplErr != nil {
			return fmt.Errorf("executing template: %w", tmplErr)
		}
		return nil
	default:
		return renderUnitTable(ios, opts.TUI, rows)
	}
}

// resolvedRow renders one resolved unit as a list row: built-in units
// show the placeholder path, registered ones their directory (with a
// missing-marker when the path no longer loads).
func resolvedRow(u monitor.ResolvedUnit) unitRow {
	row := unitRow{
		Name:   u.Name,
		Path:   u.Path,
		Source: u.Source,
		Active: activeNo,
	}
	if u.Active {
		row.Active = activeYes
	}
	if u.Path == "" {
		row.Path = cmdutil.RegistryBuiltinPath
	} else if u.LoadErr != nil || !dirExists(u.Path) {
		row.Path += pathMissingSuffix
	}
	return row
}

// dirExists reports whether a registered unit path is still a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func renderUnitTable(ios *iostreams.IOStreams, ui *tui.TUI, rows []unitRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(ios.ErrOut, "No monitoring units found.")
		return nil
	}
	table := ui.NewTable("NAME", "PATH", "SOURCE", "ACTIVE")
	for _, r := range rows {
		table.AddRow(r.Name, r.Path, r.Source, r.Active)
	}
	if err := table.Render(); err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}
	return nil
}
