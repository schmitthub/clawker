// Package inspect provides the volume inspect command.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the inspect command.
type Options struct {
	Format string
}

// NewCmd creates the volume inspect command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "inspect VOLUME [VOLUME...]",
		Short: "Display detailed information on one or more volumes",
		Long: `Returns low-level information about clawker volumes.

By default, outputs JSON. Use --format to extract specific fields.`,
		Example: `  # Inspect a volume
  clawker volume inspect clawker.myapp.ralph-workspace

  # Inspect multiple volumes
  clawker volume inspect clawker.myapp.ralph-workspace clawker.myapp.ralph-config

  # Get specific field using Go template
  clawker volume inspect --format '{{.Mountpoint}}' clawker.myapp.ralph-workspace`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args)
		},
	}

	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "Format output using a Go template")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, volumes []string) error {
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

	for _, name := range volumes {
		// Inspect the volume
		vol, err := client.VolumeInspect(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect volume %q: %w", name, err))
			continue
		}

		results = append(results, vol)
	}

	// Output results
	if len(results) > 0 {
		if opts.Format != "" {
			// TODO: Implement Go template formatting
			// For now, just output JSON
			if err := outputJSON(results); err != nil {
				return err
			}
		} else {
			if err := outputJSON(results); err != nil {
				return err
			}
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "Error: %v\n", e)
		}
		return fmt.Errorf("failed to inspect %d volume(s)", len(errs))
	}

	return nil
}

func outputJSON(data any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
