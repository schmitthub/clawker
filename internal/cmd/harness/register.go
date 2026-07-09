package harness

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// RegisterOptions holds the inputs for `clawker harness register`.
type RegisterOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Path  string // positional arg: harness bundle directory
	Name  string // --name override; empty derives from the dir name
	Force bool   // --force replaces an existing registration
}

// NewCmdHarnessRegister creates the `clawker harness register` command.
func NewCmdHarnessRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command {
	opts := &RegisterOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Path:      "",
		Name:      "",
		Force:     false,
	}

	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a harness bundle directory",
		Long: `Registers a harness bundle directory in the project's clawker.yaml.

The directory must contain a harness.yaml manifest and a Dockerfile.harness.tmpl
fragment. The harness name defaults to the directory's base name; override it
with --name. Any stack definitions the bundle embeds under stacks/ are reported.

The path is stored relative to the project root when the directory lives inside
it, otherwise as an absolute path. Registering a name that is already registered
fails unless --force is given, which replaces the entry and reports the shadowed
path. Registration writes only the harnesses.<name>.path key, so any per-harness
init config on that entry is preserved.`,
		Example: `  # Register ./tools/codex-bundle as "codex-bundle"
  clawker harness register ./tools/codex-bundle

  # Register under an explicit name
  clawker harness register ./vendor/codex --name codex

  # Replace an existing registration
  clawker harness register ./tools/codex-bundle --name codex --force`,
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
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Replace an existing registration")

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

	resolved, err := cmdutil.ResolveRegistryPath(cfg.ProjectRoot(), wd, opts.Path)
	if err != nil {
		return fmt.Errorf("harness path: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = deriveName(resolved.Abs)
	}
	if err = consts.ValidateHarnessName(name); err != nil {
		return fmt.Errorf("harness name: %w", err)
	}

	// Validate the directory is a real harness bundle (valid manifest +
	// fragment + egress floor) before touching the config.
	bundledStacks, monitoringUnits, err := loadBundleForRegister(name, resolved.Abs)
	if err != nil {
		return err
	}

	existing := cfg.Project().Harnesses[name]
	alreadyRegistered := existing.Path != ""
	if alreadyRegistered && !opts.Force {
		return fmt.Errorf(
			"harness %q is already registered (path %s) — pass --force to replace it",
			name, existing.Path)
	}

	store := cfg.ProjectStore()
	if err = store.Set(pathKey(name), resolved.Stored); err != nil {
		return fmt.Errorf("setting harness registration: %w", err)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	reportRegistered(ios, registerReport{
		name:       name,
		stored:     resolved.Stored,
		oldPath:    existing.Path,
		replaced:   alreadyRegistered,
		stacks:     bundledStacks,
		monitoring: monitoringUnits,
		bundleAbs:  resolved.Abs,
		writtenTo:  cmdutil.PrimaryWritePath(store),
	})
	return nil
}

// loadBundleForRegister loads the bundle at absPath and runs the full
// register-time validation: manifest + fragment parse ([bundler.LoadBundle],
// which also rejects a floor rule that lowers the TLS trust bar and loads
// every declared monitoring unit), embedded-stack discovery, and per-rule
// egress-floor validation. It returns the bundle's embedded stack names
// and declared monitoring units.
func loadBundleForRegister(name, absPath string) ([]string, []string, error) {
	bundle, err := bundler.LoadBundle(name, os.DirFS(absPath))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid harness bundle %s: %w", absPath, err)
	}
	bundledStacks, err := bundle.BundledStacks()
	if err != nil {
		return nil, nil, fmt.Errorf("invalid harness bundle %s: %w", absPath, err)
	}
	if err = validateFloorRules(absPath, bundle.Manifest.Egress); err != nil {
		return nil, nil, err
	}
	return bundledStacks, bundle.DeclaredMonitoringUnits(), nil
}

// validateFloorRules checks the bundle's egress floor exactly as firewall sync
// will at container create, so a malformed floor rule (bad port spec, invalid
// path) fails registration here instead of silently passing until the first
// build or run. [firewall.ValidateRule] is the same validator the launch path
// applies. The TLS-verification-skip guard is enforced earlier, at bundle load
// ([bundler.LoadBundle]).
func validateFloorRules(absPath string, rules []config.EgressRule) error {
	for _, r := range rules {
		if err := firewall.ValidateRule(r); err != nil {
			return fmt.Errorf("invalid harness bundle %s: egress floor rule %q: %w", absPath, r.Dst, err)
		}
	}
	return nil
}

// registerReport carries the fields reportRegistered prints.
type registerReport struct {
	name       string
	stored     string
	oldPath    string
	replaced   bool
	stacks     []string
	monitoring []string
	bundleAbs  string
	writtenTo  string
}

// reportRegistered prints the success line, the prior path when the
// registration overrode an existing entry (a parent-layer entry stays in its
// own file and is merely shadowed, so the old path is reported as "was", not
// "replaced"), the config file it landed in, and any stacks the bundle embeds.
func reportRegistered(ios *iostreams.IOStreams, r registerReport) {
	cs := ios.ColorScheme()
	if r.replaced && r.oldPath != r.stored {
		fmt.Fprintf(ios.Out, "%s Registered harness '%s' → %s (was %s)\n",
			cs.SuccessIcon(), r.name, r.stored, r.oldPath)
	} else {
		fmt.Fprintf(ios.Out, "%s Registered harness '%s' → %s\n", cs.SuccessIcon(), r.name, r.stored)
	}
	if r.writtenTo != "" {
		fmt.Fprintf(ios.Out, "  Written to %s\n", r.writtenTo)
	}
	if len(r.stacks) > 0 {
		fmt.Fprintf(ios.Out, "  Bundled stacks: %s\n", strings.Join(r.stacks, ", "))
	}
	// Shipping a monitoring unit never writes the host-global monitoring
	// registry (auto-add would collide with no registrar to resolve it) —
	// surface the units and the explicit promotion path instead.
	if len(r.monitoring) > 0 {
		fmt.Fprintf(ios.Out, "  Monitoring units: %s — register with "+
			"'clawker monitor register %s/%s/<name>'\n",
			strings.Join(r.monitoring, ", "), r.bundleAbs, bundler.MonitoringUnitsSubdir)
	}
}
