package units

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
)

// EnableOptions holds the inputs for `clawker monitor enable`.
type EnableOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Name string // positional arg
}

// NewCmdEnable creates the `clawker monitor enable` command.
func NewCmdEnable(f *cmdutil.Factory, runF func(context.Context, *EnableOptions) error) *cobra.Command {
	opts := &EnableOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Name:      "",
	}

	cmd := &cobra.Command{
		Use:   "enable <name>",
		Short: "Activate a monitoring unit",
		Long: `Activates a monitoring unit: its indexes, dashboards, and collector
routing are seeded into the monitoring stack on the next
'clawker monitor init && clawker monitor up'.

Every unit — including built-in ones like claude-code — is inactive until
enabled; seeding is a deliberate choice, never automatic.

Resource exclusivity is checked here: enabling a unit whose index or
service.name route collides with an already-active unit fails, naming
the conflict. Disable the other unit first to swap loadouts.`,
		Example: `  # Seed Claude Code telemetry (index, dashboards, routing)
  clawker monitor enable claude-code

  # Then apply
  clawker monitor init && clawker monitor up`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return enableRun(cmd.Context(), opts)
		},
	}

	return cmd
}

func enableRun(_ context.Context, opts *EnableOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	units, err := monitor.ResolveUnits(cfg)
	if err != nil {
		return fmt.Errorf("resolving monitoring units: %w", err)
	}
	target, known := findUnit(units, opts.Name)
	if !known {
		return fmt.Errorf(
			"monitoring unit %q is not registered or built-in — known units: %s",
			opts.Name, strings.Join(unitNames(units), ", "),
		)
	}
	if target.LoadErr != nil {
		return fmt.Errorf(
			"monitoring unit %q failed to load from %s: %w — fix the path or re-register",
			target.Name, target.Path, target.LoadErr,
		)
	}

	// Resource-exclusivity front door: validate the WOULD-BE active set
	// before writing the flag, so a collision is an error here rather
	// than a broken `monitor init` later.
	wouldBe := make([]monitor.ResolvedUnit, 0, len(units))
	for _, u := range units {
		if u.Name == opts.Name {
			u.Active = true
		}
		wouldBe = append(wouldBe, u)
	}
	if _, err = monitor.ActiveFromResolved(wouldBe); err != nil {
		return fmt.Errorf("enabling %q: %w", opts.Name, err)
	}

	store := cfg.SettingsStore()
	if err = store.Set(activeKey(opts.Name), true); err != nil {
		return fmt.Errorf("setting activation: %w", err)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	fmt.Fprintf(ios.Out, "%s Enabled monitoring unit '%s'\n", cs.SuccessIcon(), opts.Name)
	printApplyRecipe(ios)
	return nil
}

// unitNames lists resolved unit names, sorted.
func unitNames(units []monitor.ResolvedUnit) []string {
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, u.Name)
	}
	sort.Strings(names)
	return names
}
