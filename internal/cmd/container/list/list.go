package list

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"text/template"
	"time"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// ListOptions holds options for the list command.
type ListOptions struct {
	All     bool
	Project string
	Format  string
}

// NewCmdList creates the container list command.
func NewCmdList(f *cmdutil2.Factory) *cobra.Command {
	opts := &ListOptions{}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "ps"},
		Short:   "List containers",
		Long: `Lists all containers created by clawker.

By default, shows only running containers. Use -a to show all containers.

Note: Use 'clawker monitor status' for monitoring stack containers.`,
		Example: `  # List running containers
  clawker container list

  # List all containers (including stopped)
  clawker container ls -a

  # List containers for a specific project
  clawker container list -p myproject

  # List container names only
  clawker container ls -a --format '{{.Names}}'

  # Custom format showing name and status
  clawker container ls -a --format '{{.Name}} {{.Status}}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all containers (including stopped)")
	cmd.Flags().StringVarP(&opts.Project, "project", "p", "", "Filter by project name")
	cmd.Flags().StringVar(&opts.Format, "format", "", "Format output using a Go template")

	return cmd
}

func runList(ctx context.Context, f *cmdutil2.Factory, opts *ListOptions) error {
	ios := f.IOStreams

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil2.HandleError(err)
		return err
	}

	// List containers
	var containers []docker.Container
	if opts.Project != "" {
		containers, err = client.ListContainersByProject(ctx, opts.Project, opts.All)
	} else {
		containers, err = client.ListContainers(ctx, opts.All)
	}
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		if opts.All {
			fmt.Fprintln(ios.ErrOut, "No clawker containers found.")
		} else {
			fmt.Fprintln(ios.ErrOut, "No running clawker containers found. Use -a to show all containers.")
		}
		return nil
	}

	// Output with format if specified
	if opts.Format != "" {
		return outputFormatted(ios.Out, opts.Format, containers)
	}

	// Print table
	w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tPROJECT\tAGENT\tIMAGE\tCREATED")

	for _, c := range containers {
		created := formatCreatedTime(c.Created)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Name,
			c.Status,
			c.Project,
			c.Agent,
			truncateImage(c.Image),
			created,
		)
	}

	return w.Flush()
}

// formatCreatedTime formats a Unix timestamp into a human-readable relative time.
func formatCreatedTime(timestamp int64) string {
	created := time.Unix(timestamp, 0)
	duration := time.Since(created)

	switch {
	case duration < time.Minute:
		return "Less than a minute ago"
	case duration < time.Hour:
		mins := int(duration.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// truncateImage shortens long image names.
func truncateImage(image string) string {
	const maxLen = 40
	if len(image) <= maxLen {
		return image
	}
	return image[:maxLen-3] + "..."
}

// containerForFormat wraps Container with Docker-compatible field aliases.
// Docker CLI uses {{.Names}} (plural) while clawker uses {{.Name}} (singular).
// This wrapper provides both for compatibility.
type containerForFormat struct {
	docker.Container
	Names string // Alias for .Name to match Docker CLI's {{.Names}}
}

// outputFormatted outputs containers using a Go template format string.
func outputFormatted(w io.Writer, format string, containers []docker.Container) error {
	tmpl, err := template.New("format").Parse(format)
	if err != nil {
		return fmt.Errorf("invalid format template: %w", err)
	}

	for _, c := range containers {
		// Wrap with Docker-compatible aliases
		wrapper := containerForFormat{
			Container: c,
			Names:     c.Name,
		}
		if err := tmpl.Execute(w, wrapper); err != nil {
			return fmt.Errorf("template execution failed: %w", err)
		}
		fmt.Fprintln(w)
	}
	return nil
}
