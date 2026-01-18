// Package list provides the network list command.
package list

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the list command.
type Options struct {
	Quiet bool
}

// NewCmd creates the network list command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List networks",
		Long: `Lists all networks created by clawker.

Networks are used for container communication and monitoring stack
integration. The primary network is clawker-net.`,
		Example: `  # List all clawker networks
  clawker network list

  # List networks (short form)
  clawker network ls

  # List network names only
  clawker network ls -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display network names")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// List networks
	networks, err := client.NetworkList(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	if len(networks.Items) == 0 {
		fmt.Fprintln(os.Stderr, "No clawker networks found.")
		return nil
	}

	// Quiet mode - just print names
	if opts.Quiet {
		for _, n := range networks.Items {
			fmt.Println(n.Name)
		}
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NETWORK ID\tNAME\tDRIVER\tSCOPE")

	for _, n := range networks.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			truncateID(n.ID),
			n.Name,
			n.Driver,
			n.Scope,
		)
	}

	return w.Flush()
}

// truncateID shortens a Docker ID to 12 characters.
func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
