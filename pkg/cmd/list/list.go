package list

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/schmitthub/clawker/internal/engine"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/logger"
	"github.com/spf13/cobra"
)

// ListOptions contains the options for the list command.
type ListOptions struct {
	All     bool   // -a, --all: show stopped containers too
	Project string // -p, --project: filter by project
}

// NewCmdList creates the list command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	opts := &ListOptions{}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "ps"},
		Short:   "List clawker containers",
		Long: `Lists all containers created by clawker.

By default, shows only running containers. Use -a to show all containers.

Note: Use 'clawker monitor status' for monitoring stack containers.`,
		Example: `  # List running containers
  clawker list

  # List all containers (including stopped)
  clawker list -a

  # List containers for a specific project
  clawker list -p myproject`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all containers (including stopped)")
	cmd.Flags().StringVarP(&opts.Project, "project", "p", "", "Filter by project name")

	return cmd
}

func runList(_ *cmdutil.Factory, opts *ListOptions) error {
	ctx := context.Background()

	// Connect to Docker
	eng, err := engine.NewEngine(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer eng.Close()

	// List containers
	var containers []engine.ClawkerContainer
	if opts.Project != "" {
		containers, err = eng.ListClawkerContainersByProject(ctx, opts.Project, opts.All)
	} else {
		containers, err = eng.ListClawkerContainers(ctx, opts.All)
	}
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		if opts.All {
			fmt.Fprintln(os.Stderr, "No clawker containers found.")
		} else {
			fmt.Fprintln(os.Stderr, "No running clawker containers found. Use -a to show all containers.")
		}
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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

	if err := w.Flush(); err != nil {
		logger.Warn().Err(err).Msg("failed to flush output")
	}

	return nil
}

// formatCreatedTime formats a Unix timestamp as a human-readable relative time
func formatCreatedTime(created int64) string {
	if created == 0 {
		return "unknown"
	}

	t := time.Unix(created, 0)
	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
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
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		weeks := int(duration.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	}
}

// truncateImage truncates long image names for display
func truncateImage(image string) string {
	if len(image) <= 40 {
		return image
	}
	return image[:37] + "..."
}
