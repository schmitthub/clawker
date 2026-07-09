package units

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RegisterOptions holds the inputs for `clawker monitor register`.
type RegisterOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Path  string // positional arg: monitoring unit directory
	Name  string // --name override; empty derives from the dir name
	Force bool   // --force updates an existing registered entry's path
}

// NewCmdRegister creates the `clawker monitor register` command.
func NewCmdRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command {
	opts := &RegisterOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Path:      "",
		Name:      "",
		Force:     false,
	}

	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a monitoring unit directory",
		Long: `Registers a monitoring unit directory in the host-global registry
(settings.yaml).

The directory must contain a monitoring.yaml manifest plus the artifact
files it declares (index templates, ingest pipelines, dashboards). The
unit name defaults to the directory's base name; override it with --name.

The registry is host-global, so the path is always stored absolute.
Registration makes a unit available — it does not seed anything. Activate
it with 'clawker monitor enable <name>'.

Unit names are a flat namespace with no override semantics: a name held
by a built-in unit (shipped inside an embedded harness bundle) cannot be
registered at all — choose another name with --name. --force only updates
the path of your own existing registered entry.`,
		Example: `  # Register a unit from a harness bundle's monitoring/ dir
  clawker monitor register ~/tools/codex-bundle/monitoring/codex-usage

  # Register under an explicit name
  clawker monitor register ./observability/codex --name codex-usage

  # Update an existing registration's path
  clawker monitor register /new/path/codex-usage --force`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Path = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return registerRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Name, "name", "", "Registry name (defaults to the directory base name)")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Update an existing registered entry's path")

	return cmd
}

func registerRun(_ context.Context, opts *RegisterOptions) error {
	ios := opts.IOStreams

	cfg, err := opts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving working directory: %w", err)
	}

	// Empty project root: the monitoring registry is host-global, so the
	// stored form is always the absolute path — never project-relative.
	resolved, err := cmdutil.ResolveRegistryPath("", wd, opts.Path)
	if err != nil {
		return fmt.Errorf("monitoring unit path: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = deriveName(resolved.Abs)
	}
	if err = consts.ValidateName(name); err != nil {
		return fmt.Errorf("monitoring unit name: %w", err)
	}

	// Validate the directory is a real unit BEFORE touching settings.
	if _, err = bundler.LoadMonitoringUnit(name, os.DirFS(resolved.Abs)); err != nil {
		return fmt.Errorf("invalid monitoring unit directory %s: %w", resolved.Abs, err)
	}

	// Flat namespace, no override semantics: a built-in name is never
	// registrable (--force does not apply); an existing registered entry
	// is only updated under --force.
	existing, err := checkNameAvailable(cfg, name, opts.Force)
	if err != nil {
		return err
	}

	store := cfg.SettingsStore()
	if err = store.Set(pathKey(name), resolved.Stored); err != nil {
		return fmt.Errorf("setting monitoring unit registration: %w", err)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	reportRegistered(ios, name, resolved.Stored, existing, cmdutil.PrimaryWritePath(store))
	return nil
}

// reportRegistered prints the success line, the prior path on a --force
// update, the settings file written, and the enable hint.
func reportRegistered(ios *iostreams.IOStreams, name, stored, oldPath, writtenTo string) {
	cs := ios.ColorScheme()
	if oldPath != "" && oldPath != stored {
		fmt.Fprintf(ios.Out, "%s Registered monitoring unit '%s' → %s (was %s)\n",
			cs.SuccessIcon(), name, stored, oldPath)
	} else {
		fmt.Fprintf(ios.Out, "%s Registered monitoring unit '%s' → %s\n",
			cs.SuccessIcon(), name, stored)
	}
	if writtenTo != "" {
		fmt.Fprintf(ios.Out, "  Written to %s\n", writtenTo)
	}
	fmt.Fprintf(ios.Out, "  Inactive — enable with 'clawker monitor enable %s'\n", name)
}

// checkNameAvailable enforces the registry's flat namespace: a built-in
// name is never registrable; an existing registered entry needs --force.
// Returns the existing entry's path ("" when new).
func checkNameAvailable(cfg config.Config, name string, force bool) (string, error) {
	builtIn, err := isBuiltInUnit(name)
	if err != nil {
		return "", err
	}
	if builtIn {
		return "", fmt.Errorf(
			"name %q is a built-in monitoring unit — choose another name with --name "+
				"(to replace its loadout: 'clawker monitor disable %s' and enable yours)",
			name, name,
		)
	}
	existing, isRegistered := cfg.Settings().Monitoring.Units[name]
	if isRegistered && !force {
		return "", fmt.Errorf(
			"monitoring unit %q is already registered (path %s) — pass --force to update it",
			name, existing.Path)
	}
	return existing.Path, nil
}
