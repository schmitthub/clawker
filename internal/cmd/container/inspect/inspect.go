package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/template"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	Agent  bool
	Format string
	Size   bool

	containers []string
}

// NewCmdInspect creates the container inspect command.
func NewCmdInspect(f *cmdutil.Factory) *cobra.Command {
	opts := &InspectOptions{}

	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] CONTAINER [CONTAINER...]",
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
			opts.containers = args
			return runInspect(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Use agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "Format output using a Go template")
	cmd.Flags().BoolVarP(&opts.Size, "size", "s", false, "Display total file sizes")

	return cmd
}

func runInspect(ctx context.Context, f *cmdutil.Factory, opts *InspectOptions) error {
	ios := f.IOStreams

	// Resolve container names
	containers := opts.containers
	if opts.Agent {
		var err error
		containers, err = cmdutil.ResolveContainerNamesFromAgents(f, containers)
		if err != nil {
			return err
		}
	}

	// Connect to Docker
	dockerClient, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
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
		info, err := dockerClient.ContainerInspect(ctx, c.ID, docker.ContainerInspectOptions{})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to inspect container %q: %w", name, err))
			continue
		}

		results = append(results, info)
	}

	// Output results
	if len(results) > 0 {
		if opts.Format != "" {
			return outputFormatted(ios.Out, opts.Format, results)
		}
		if err := outputJSON(ios.Out, results); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(ios.ErrOut, "Error: %v\n", e)
		}
		return fmt.Errorf("failed to inspect %d container(s)", len(errs))
	}

	return nil
}

func outputJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")
	return encoder.Encode(data)
}

// outputFormatted outputs results using a Go template format string.
// Templates execute against the Container field (InspectResponse) for Docker CLI compatibility.
// This means templates like '{{.State.Status}}' work as expected.
func outputFormatted(w io.Writer, format string, results []docker.ContainerInspectResult) error {
	tmpl, err := template.New("format").Parse(format)
	if err != nil {
		return fmt.Errorf("invalid format template: %w", err)
	}

	for _, result := range results {
		// Execute against .Container (InspectResponse) for Docker CLI compatibility
		// This allows templates like '{{.State.Status}}' instead of '{{.Container.State.Status}}'
		if err := tmpl.Execute(w, result.Container); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		fmt.Fprintln(w)
	}
	return nil
}
