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

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	Format string
	Size   bool
}

// NewCmdInspect creates the container inspect command.
func NewCmdInspect(f *cmdutil.Factory) *cobra.Command {
	opts := &InspectOptions{}

	cmd := &cobra.Command{
		Use:   "inspect CONTAINER [CONTAINER...]",
		Short: "Display detailed information on one or more containers",
		Long: `Returns low-level information about clawker containers.

By default, outputs JSON. Use --format to extract specific fields.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Inspect a container
  clawker container inspect clawker.myapp.ralph

  # Inspect multiple containers
  clawker container inspect clawker.myapp.ralph clawker.myapp.writer

  # Get specific field using Go template
  clawker container inspect --format '{{.State.Status}}' clawker.myapp.ralph

  # Get container IP address
  clawker container inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' clawker.myapp.ralph`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(f, opts, args)
		},
	}

	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "Format output using a Go template")
	cmd.Flags().BoolVarP(&opts.Size, "size", "s", false, "Display total file sizes")

	return cmd
}

func runInspect(_ *cmdutil.Factory, opts *InspectOptions, containers []string) error {
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

	for _, name := range containers {
		// Find container by name
		c, err := client.FindContainerByName(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find container %q: %w", name, err))
			continue
		}
		if c == nil {
			errs = append(errs, fmt.Errorf("container %q not found", name))
			continue
		}

		// Inspect the container
		info, err := client.ContainerInspect(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect container %q: %w", name, err))
			continue
		}

		results = append(results, info)
	}

	// Output results
	if len(results) > 0 {
		if opts.Format != "" {
			// TODO: Implement Go template formatting
			// For now, just output JSON
			return outputJSON(results)
		}
		if err := outputJSON(results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "Error: %v\n", e)
		}
		return fmt.Errorf("failed to inspect %d container(s)", len(errs))
	}

	return nil
}

func outputJSON(data any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}
