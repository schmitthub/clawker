package list

import (
	"context"
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// Alias source values reported in the SOURCE column / JSON output.
const (
	sourceDefault = "default"
	sourceUser    = "user"
)

// disabledDisplay marks an alias whose expansion is empty (disabled).
const disabledDisplay = "(disabled)"

// ListOptions holds dependencies for the alias list command.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Config    func() (config.Config, error)
	Format    *cmdutil.FormatFlags
}

type aliasRow struct {
	Name      string `json:"name"`
	Expansion string `json:"expansion"`
	Source    string `json:"source"`
}

// NewCmdList creates the `clawker alias list` command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured command aliases",
		Long: `Lists all configured command aliases with their expansions.

The SOURCE column distinguishes shipped defaults from user-defined
aliases. An alias with an empty expansion is disabled.`,
		Example: `  # List aliases
  clawker alias list

  # Output as JSON
  clawker alias list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	cmd.Flags().Lookup("quiet").Usage = "Only display alias names"

	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		return err
	}
	aliases := cfg.Settings().Aliases
	if len(aliases) == 0 {
		fmt.Fprintln(ios.ErrOut, "No aliases configured.")
		fmt.Fprintln(ios.ErrOut, "Use 'clawker alias set <alias> <expansion>' to create one.")
		return nil
	}

	defaults, err := shared.DefaultAliases()
	if err != nil {
		return err
	}

	rows := buildAliasRows(aliases, defaults)

	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Name)
		}
		return nil

	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

	default:
		tp := opts.TUI.NewTable("NAME", "EXPANSION", "SOURCE")
		cs := ios.ColorScheme()
		for _, r := range rows {
			expansion := r.Expansion
			if expansion == "" {
				expansion = cs.Muted(disabledDisplay)
			}
			tp.AddRow(r.Name, expansion, r.Source)
		}
		return tp.Render()
	}
}

// buildAliasRows converts the merged alias map into sorted display rows.
// An alias counts as a default while its expansion matches the shipped
// value or is disabled (a disabled default is still the default alias);
// an overridden default reports as user.
func buildAliasRows(aliases, defaults map[string]string) []aliasRow {
	rows := make([]aliasRow, 0, len(aliases))
	for name, expansion := range aliases {
		source := sourceUser
		if def, ok := defaults[name]; ok && (def == expansion || expansion == "") {
			source = sourceDefault
		}
		rows = append(rows, aliasRow{Name: name, Expansion: expansion, Source: source})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}
