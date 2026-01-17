// Package inspect provides the network inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the inspect command.
type Options struct {
	// Format is reserved for future Go template support
	Verbose bool
}

// NewCmd creates the network inspect command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

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
			return run(f, opts, args)
		},
	}

	cmd.Flags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Verbose output for swarm networks")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, networks []string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	var results []any
	var errs []error

	for _, name := range networks {
		// Inspect the network
		net, err := client.NetworkInspect(ctx, name, dockerclient.NetworkInspectOptions{
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
		if err := outputJSON(results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			cmdutil.HandleError(e)
		}
		return fmt.Errorf("failed to inspect %d network(s)", len(errs))
	}

	return nil
}

func outputJSON(data any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
