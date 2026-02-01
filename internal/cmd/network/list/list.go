// Package list provides the network list command.
package list

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// ListOptions holds options for the list command.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(ctx context.Context) (*docker.Client, error)

	Quiet bool
}

// NewCmdList creates the network list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List networks",
		Long: `Lists all networks created by clawker.

Networks are used for container communication and monitoring stack
internals. The primary network is clawker-net.`,
		Example: `  # List all clawker networks
  clawker network list

  # List networks (short form)
  clawker network ls

  # List network names only
  clawker network ls -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display network names")

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		cmdutil.HandleError(opts.IOStreams, err)
		return err
	}

	// List networks
	networks, err := client.NetworkList(ctx)
	if err != nil {
		cmdutil.HandleError(opts.IOStreams, err)
		return err
	}

	if len(networks.Items) == 0 {
		fmt.Fprintln(opts.IOStreams.ErrOut, "No clawker networks found.")
		return nil
	}

	// Quiet mode - just print names
	if opts.Quiet {
		for _, n := range networks.Items {
			fmt.Fprintln(opts.IOStreams.Out, n.Name)
		}
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(opts.IOStreams.Out, 0, 0, 2, ' ', 0)
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
