// Package inspect provides the network inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(ctx context.Context) (*docker.Client, error)

	Networks []string
	Verbose  bool
}

// NewCmdInspect creates the network inspect command.
func NewCmdInspect(f *cmdutil.Factory, runF func(context.Context, *InspectOptions) error) *cobra.Command {
	opts := &InspectOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

	cmd := &cobra.Command{
		Use:   "inspect NETWORK [NETWORK...]",
		Short: "Display detailed information on one or more networks",
		Long: `Returns low-level information about clawker networks.

Outputs detailed network information in JSON format, including
connected containers and configuration.`,
		Example: `  # Inspect a network
  clawker network inspect clawker-net

  # Inspect multiple networks
  clawker network inspect clawker-net myapp-net

  # Inspect with verbose output (includes services and tasks)
  clawker network inspect --verbose clawker-net`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Networks = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return inspectRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Verbose output for swarm networks")

	return cmd
}

func inspectRun(ctx context.Context, opts *InspectOptions) error {
	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(opts.IOStreams, err)
		return err
	}

	var results []any
	var errs []error

	for _, name := range opts.Networks {
		// Inspect the network
		net, err := client.NetworkInspect(ctx, name, docker.NetworkInspectOptions{
			Verbose: opts.Verbose,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect network %q: %w", name, err))
			continue
		}

		results = append(results, net)
	}

	// Output results
	if len(results) > 0 {
		if err := outputJSON(opts.IOStreams.Out, results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			cmdutil.HandleError(opts.IOStreams, e)
		}
		return fmt.Errorf("failed to inspect %d network(s)", len(errs))
	}

	return nil
}

func outputJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
