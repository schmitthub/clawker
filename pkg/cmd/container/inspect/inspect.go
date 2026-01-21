package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/template"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	Agent  string
	Format string
	Size   bool
}

// NewCmdInspect creates the container inspect command.
func NewCmdInspect(f *cmdutil.Factory) *cobra.Command {
	opts := &InspectOptions{}

	cmd := &cobra.Command{
		Use:   "inspect [CONTAINER...]",
		Short: "Display detailed information on one or more containers",
		Long: `Returns low-level information about clawker containers.

By default, outputs JSON. Use --format to extract specific fields.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Inspect a container using agent name
  clawker container inspect --agent ralph

  # Inspect a container by full name
  clawker container inspect clawker.myapp.ralph

  # Inspect multiple containers
  clawker container inspect clawker.myapp.ralph clawker.myapp.writer

  # Get specific field using Go template
  clawker container inspect --format '{{.State.Status}}' --agent ralph

  # Get container IP address
  clawker container inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' --agent ralph`,
		Args: cmdutil.AgentArgsValidator(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(f, opts, args)
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "Format output using a Go template")
	cmd.Flags().BoolVarP(&opts.Size, "size", "s", false, "Display total file sizes")

	return cmd
}

func runInspect(f *cmdutil.Factory, opts *InspectOptions, args []string) error {
	ctx := context.Background()

	// Resolve container names
	containers, err := cmdutil.ResolveContainerNames(f, opts.Agent, args)
	if err != nil {
		return err
	}

	// Connect to Docker
	dockerClient, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	var results []docker.ContainerInspectResult
	var errs []error

	for _, name := range containers {
		// Find container by name
		c, err := dockerClient.FindContainerByName(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find container %q: %w", name, err))
			continue
		}
		if c == nil {
			errs = append(errs, fmt.Errorf("container %q not found", name))
			continue
		}

		// Inspect the container
		info, err := dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect container %q: %w", name, err))
			continue
		}

		results = append(results, info)
	}

	// Output results
	if len(results) > 0 {
		if opts.Format != "" {
			return outputFormatted(opts.Format, results)
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

// outputFormatted outputs results using a Go template format string.
// Templates execute against the Container field (InspectResponse) for Docker CLI compatibility.
// This means templates like '{{.State.Status}}' work as expected.
func outputFormatted(format string, results []docker.ContainerInspectResult) error {
	tmpl, err := template.New("format").Parse(format)
	if err != nil {
		return fmt.Errorf("invalid format template: %w", err)
	}

	for _, result := range results {
		// Execute against .Container (InspectResponse) for Docker CLI compatibility
		// This allows templates like '{{.State.Status}}' instead of '{{.Container.State.Status}}'
		if err := tmpl.Execute(os.Stdout, result.Container); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		fmt.Println()
	}
	return nil
}
