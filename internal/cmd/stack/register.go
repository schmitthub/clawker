package stack

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/stack"
)

// RegisterOptions holds the inputs for `clawker stack register`.
type RegisterOptions struct {
	IOStreams *iostreams.IOStreams
	Config    func() (config.Config, error)

	Path  string // positional arg: stack definition directory
	Name  string // --name override; empty derives from the dir name
	Force bool   // --force replaces an existing registration
}

// NewCmdStackRegister creates the `clawker stack register` command.
func NewCmdStackRegister(f *cmdutil.Factory, runF func(context.Context, *RegisterOptions) error) *cobra.Command {
	opts := &RegisterOptions{
		IOStreams: f.IOStreams,
		Config:    f.Config,
		Path:      "",
		Name:      "",
		Force:     false,
	}

	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a stack definition directory",
		Long: `Registers a stack definition directory in the project's clawker.yaml.

The directory must contain a stack.yaml manifest and at least one Dockerfile
fragment (Dockerfile.stack-root.tmpl and/or Dockerfile.stack-user.tmpl). The
stack name defaults to the directory's base name; override it with --name.

The path is stored relative to the project root when the directory lives inside
it (so the registry entry stays portable within the project), otherwise as an
absolute path. Registering a name that is already registered fails unless
--force is given, which replaces the entry and reports the shadowed path.`,
		Example: `  # Register ./stacks/my-rust as "my-rust"
  clawker stack register ./stacks/my-rust

  # Register under an explicit name
  clawker stack register ./vendor/rustup --name rust

  # Replace an existing registration
  clawker stack register ./stacks/my-rust --force`,
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
		return fmt.Errorf("stack path: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = deriveName(resolved.Abs)
	}
	if err = consts.ValidateName(name); err != nil {
		return fmt.Errorf("stack name: %w", err)
	}

	// Validate the directory is a real stack definition (stack.yaml + >=1
	// fragment) before touching the config file.
	if _, err = stack.Load(name, os.DirFS(resolved.Abs)); err != nil {
		return fmt.Errorf("invalid stack directory %s: %w", resolved.Abs, err)
	}

	existing, isRegistered := cfg.Project().Stacks[name]
	if isRegistered && !opts.Force {
		return fmt.Errorf(
			"stack %q is already registered (path %s) — pass --force to replace it",
			name, existing.Path)
	}

	store := cfg.ProjectStore()
	if err = store.Set(pathKey(name), resolved.Stored); err != nil {
		return fmt.Errorf("setting stack registration: %w", err)
	}
	if err = store.Write(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	reportRegistered(ios, name, resolved.Stored, existing.Path, isRegistered, cmdutil.PrimaryWritePath(store))
	return nil
}

// reportRegistered prints the success line, the replaced path when the
// registration overwrote an existing one, and the config file it landed in.
func reportRegistered(ios *iostreams.IOStreams, name, stored, oldPath string, replaced bool, writtenTo string) {
	cs := ios.ColorScheme()
	if replaced {
		fmt.Fprintf(ios.Out, "%s Registered stack '%s' → %s (replaced %s)\n",
			cs.SuccessIcon(), name, stored, oldPath)
	} else {
		fmt.Fprintf(ios.Out, "%s Registered stack '%s' → %s\n", cs.SuccessIcon(), name, stored)
	}
	if writtenTo != "" {
		fmt.Fprintf(ios.Out, "  Written to %s\n", writtenTo)
	}
}
