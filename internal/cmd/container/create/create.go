// Package create provides the container create command.
package create

import (
	"context"
	"fmt"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CreateOptions holds options for the create command.
// It embeds ContainerOptions for shared container configuration.
type CreateOptions struct {
	*copts.ContainerOptions

	IOStreams   *iostreams.IOStreams
	TUI         *tui.TUI
	Client      func(context.Context) (*docker.Client, error)
	Config      func() *config.Config
	Prompter    func() *prompter.Prompter
	Initializer *shared.ContainerInitializer

	// flags stores the pflag.FlagSet for detecting explicitly changed flags
	flags *pflag.FlagSet
}

// NewCmdCreate creates a new container create command.
func NewCmdCreate(f *cmdutil.Factory, runF func(context.Context, *CreateOptions) error) *cobra.Command {
	containerOpts := copts.NewContainerOptions()
	opts := &CreateOptions{
		ContainerOptions: containerOpts,
		IOStreams:        f.IOStreams,
		TUI:              f.TUI,
		Client:           f.Client,
		Config:           f.Config,
		Prompter:         f.Prompter,
		Initializer:      shared.NewContainerInitializer(f),
	}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create a new container",
		Long: `Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml. When --name is provided, it overrides this.

If IMAGE is "@", clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag`,
		Example: `  # Create a container with a specific agent name
  clawker container create --agent myagent alpine

  # Create a container using default image from config
  clawker container create --agent myagent @

  # Create a container with a command
  clawker container create --agent worker alpine echo "hello world"

  # Create a container with environment variables and ports
  clawker container create --agent web -e PORT=8080 -p 8080:8080 node:20

  # Create a container with a bind mount
  clawker container create --agent dev -v /host/path:/container/path alpine

  # Create an interactive container with TTY
  clawker container create -it --agent shell alpine sh`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerOpts.Image = args[0]
			if len(args) > 1 {
				containerOpts.Command = args[1:]
			}
			opts.flags = cmd.Flags()
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return createRun(cmd.Context(), opts)
		},
	}

	// Add shared container flags
	copts.AddFlags(cmd.Flags(), containerOpts)
	copts.MarkMutuallyExclusive(cmd)

	// Stop parsing flags after the first positional argument (IMAGE).
	// This allows flags after IMAGE to be passed to the container command.
	// Example: clawker create alpine --version
	//   - "alpine" is IMAGE
	//   - "--version" is passed to the container (not parsed as clawker flag)
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func createRun(ctx context.Context, opts *CreateOptions) error {
	ios := opts.IOStreams
	containerOpts := opts.ContainerOptions
	cfgGateway := opts.Config()
	cfg := cfgGateway.Project

	// --- Phase A: Pre-progress (synchronous) ---

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	if containerOpts.Image == "@" {
		resolvedImage, err := client.ResolveImageWithSource(ctx)
		if err != nil {
			return err
		}
		if resolvedImage == nil {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s No image specified and no default image configured\n", cs.FailureIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. Specify an image: clawker container create IMAGE")
			fmt.Fprintln(ios.ErrOut, "  2. Set default_image in clawker.yaml")
			fmt.Fprintln(ios.ErrOut, "  3. Set default_image in ~/.local/clawker/settings.yaml")
			fmt.Fprintln(ios.ErrOut, "  4. Build a project image: clawker build")
			return cmdutil.SilentError
		}

		if resolvedImage.Source == docker.ImageSourceDefault {
			exists, err := client.ImageExists(ctx, resolvedImage.Reference)
			if err != nil {
				logger.Warn().Err(err).Str("image", resolvedImage.Reference).Msg("failed to check if image exists")
			} else if !exists {
				if err := shared.RebuildMissingDefaultImage(ctx, shared.RebuildMissingImageOpts{
					ImageRef:       resolvedImage.Reference,
					IOStreams:      ios,
					TUI:            opts.TUI,
					Prompter:       opts.Prompter,
					SettingsLoader: func() config.SettingsLoader { return cfgGateway.SettingsLoader() },
					BuildImage:     client.BuildDefaultImage,
					CommandVerb:    "create",
				}); err != nil {
					return err
				}
			}
		}

		containerOpts.Image = resolvedImage.Reference
	}

	// Defensive check: --name and --agent should not both be set
	if containerOpts.Name != "" && containerOpts.Agent != "" && containerOpts.Name != containerOpts.Agent {
		return fmt.Errorf("cannot use both --name and --agent")
	}

	// --- Phase B: Progress-tracked initialization ---

	initResult, err := opts.Initializer.Run(ctx, shared.InitParams{
		Client:           client,
		Config:           cfg,
		ContainerOptions: containerOpts,
		Flags:            opts.flags,
		Image:            containerOpts.Image,
		StartAfterCreate: false,
	})
	if err != nil {
		return err
	}

	// --- Phase C: Post-progress ---

	for _, warning := range initResult.Warnings {
		fmt.Fprintln(ios.ErrOut, warning)
	}

	fmt.Fprintln(ios.Out, initResult.ContainerID[:12])
	return nil
}
