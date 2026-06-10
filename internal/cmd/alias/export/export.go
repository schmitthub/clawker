package export

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// ExportOptions holds dependencies for the alias export command.
type ExportOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)
}

// NewCmdExport creates the `clawker alias export` command.
func NewCmdExport(f *cmdutil.Factory, runF func(context.Context, *ExportOptions) error) *cobra.Command {
	opts := &ExportOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export aliases to the project config",
		Long: `Export active command aliases into the project config's aliases key.

Writes the current alias set into the most local project config file
discovered in the walk-up, so the aliases are version-controlled with
the project. Export never creates a new file — it requires an existing
project config (see 'clawker init'). Disabled aliases and shipped
defaults are not exported, and entries the target file already
provides are left as they are.`,
		Example: `  # Share your aliases with the team
  clawker alias export`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return exportRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func exportRun(_ context.Context, opts *ExportOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	target, err := shared.ExportTarget(cfg)
	if err != nil {
		return err
	}

	// Collect aliases worth publishing: skip disabled entries, shipped
	// defaults (never baked into project files), and entries whose winning
	// value already lives in the target itself.
	active := cfg.Project().Aliases
	exported := make(map[string]string, len(active))
	names := make([]string, 0, len(active))
	for name, expansion := range active {
		if expansion == "" {
			continue // disabled
		}
		winner, ok := cfg.ProjectStore().Provenance(shared.AliasFieldPath(name))
		if !ok || winner.Path == "" {
			continue // shipped default — never published into project files
		}
		if shared.SamePath(winner.Path, target) {
			continue // target already provides this value
		}
		exported[name] = expansion
		names = append(names, name)
	}
	if len(names) == 0 {
		fmt.Fprintln(ios.ErrOut, "No aliases to export.")
		return nil
	}
	sort.Strings(names)

	if err := shared.WriteAliases(ios.Out, target, func(m map[string]string) {
		for name, expansion := range exported {
			m[name] = expansion
		}
	}); err != nil {
		return err
	}
	fmt.Fprintf(ios.Out, "%s Exported %d alias(es): %s\n", cs.SuccessIcon(), len(names), strings.Join(names, ", "))
	return nil
}
