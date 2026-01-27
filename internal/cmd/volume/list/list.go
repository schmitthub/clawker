// Package list provides the volume list command.
package list

import (
	"context"
	"fmt"
	"text/tabwriter"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the list command.
type Options struct {
	Quiet bool
}

// NewCmd creates the volume list command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List volumes",
		Long: `Lists all volumes created by clawker.

Volumes are used to persist data between container runs, including:
  - Workspace data (in snapshot mode)
  - Configuration files
  - Command history`,
		Example: `  # List all clawker volumes
  clawker volume list

  # List volumes (short form)
  clawker volume ls

  # List volume names only
  clawker volume ls -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display volume names")

	return cmd
}

func run(f *cmdutil2.Factory, opts *Options) error {
	ctx := context.Background()
	ios := f.IOStreams

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	// List volumes
	resp, err := client.VolumeList(ctx)
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	if len(resp.Items) == 0 {
		fmt.Fprintln(ios.ErrOut, "No clawker volumes found.")
		return nil
	}

	// Quiet mode - just print names
	if opts.Quiet {
		for _, v := range resp.Items {
			fmt.Fprintln(ios.Out, v.Name)
		}
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VOLUME NAME\tDRIVER\tMOUNTPOINT")

	for _, v := range resp.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			v.Name,
			v.Driver,
			truncateMountpoint(v.Mountpoint),
		)
	}

	return w.Flush()
}

// truncateMountpoint shortens long mountpoint paths.
func truncateMountpoint(path string) string {
	const maxLen = 50
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}
