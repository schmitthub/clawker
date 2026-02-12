package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/template"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// InspectOptions holds options for the inspect command.
type InspectOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent  bool
	Format string
	Size   bool

	Containers []string
}

// NewCmdInspect creates the container inspect command.
func NewCmdInspect(f *cmdutil.Factory, runF func(context.Context, *InspectOptions) error) *cobra.Command {
	opts := &InspectOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

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
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return inspectRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Use agent name (resolves to clawker.<project>.<agent>)")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "Format output using a Go template")
	cmd.Flags().BoolVarP(&opts.Size, "size", "s", false, "Display total file sizes")

	return cmd
}

func inspectRun(ctx context.Context, opts *InspectOptions) error {
	ios := opts.IOStreams

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		resolved, err := docker.ContainerNamesFromAgents(opts.Config().Resolution.ProjectKey, containers)
		if err != nil {
			return err
		}
		containers = resolved
	}

	// Connect to Docker
	dockerClient, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
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
		cs := ios.ColorScheme()
		for _, e := range errs {
			fmt.Fprintf(ios.ErrOut, "%s %v\n", cs.FailureIcon(), e)
		}
		return cmdutil.SilentError
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
	funcMap := template.FuncMap{
		"json": func(v any) string {
			b, err := json.Marshal(v)
			if err != nil {
				return err.Error()
			}
			return string(b)
		},
	}
	tmpl, err := template.New("format").Funcs(funcMap).Parse(format)
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
