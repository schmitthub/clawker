package list

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

var containerListValidFilterKeys = []string{"name", "status", "agent"}

// ListOptions holds options for the list command.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI      *tui.TUI
	Client   func(context.Context) (*docker.Client, error)

	Format  *cmdutil.FormatFlags
	Filter  *cmdutil.FilterFlags
	All     bool
	Project string
}

// containerRow is the display/serialization type for format dispatch.
type containerRow struct {
	Name    string `json:"name"`
	Names   string `json:"names"` // Docker CLI compatibility alias
	Status  string `json:"status"`
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Image   string `json:"image"`
	Created string `json:"created"`
}

// NewCmdList creates the container list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:      f.TUI,
		Client:   f.Client,
	}

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
  clawker container ls -q

  # Output as JSON
  clawker container ls --json

  # Custom Go template
  clawker container ls --format '{{.Name}} {{.Status}}'

  # Filter by status
  clawker container ls -a --filter status=running

  # Filter by agent name
  clawker container ls --filter agent=ralph`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	opts.Filter = cmdutil.AddFilterFlags(cmd)
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all containers (including stopped)")
	cmd.Flags().StringVarP(&opts.Project, "project", "p", "", "Filter by project name")

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	// Parse and validate filters.
	filters, err := opts.Filter.Parse()
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateFilterKeys(filters, containerListValidFilterKeys); err != nil {
		return err
	}

	// Connect to Docker.
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	// Fetch containers — project flag is a server-side filter via Docker API.
	var containers []docker.Container
	if opts.Project != "" {
		containers, err = client.ListContainersByProject(ctx, opts.Project, opts.All)
	} else {
		containers, err = client.ListContainers(ctx, opts.All)
	}
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	// Apply local filters.
	containers = applyContainerFilters(containers, filters)

	// Handle empty results.
	if len(containers) == 0 {
		if opts.All {
			fmt.Fprintln(ios.ErrOut, "No clawker containers found.")
		} else {
			fmt.Fprintln(ios.ErrOut, "No running clawker containers found. Use -a to show all containers.")
		}
		return nil
	}

	// Build display rows.
	rows := buildContainerRows(containers)

	// Format dispatch.
	switch {
	case opts.Format.Quiet:
		for _, c := range containers {
			fmt.Fprintln(ios.Out, c.Name)
		}
		return nil

	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

	default:
		tp := opts.TUI.NewTable("NAME", "STATUS", "PROJECT", "AGENT", "IMAGE", "CREATED")
		for _, r := range rows {
			tp.AddRow(r.Name, r.Status, r.Project, r.Agent, r.Image, r.Created)
		}
		return tp.Render()
	}
}

func buildContainerRows(containers []docker.Container) []containerRow {
	rows := make([]containerRow, 0, len(containers))
	for _, c := range containers {
		rows = append(rows, containerRow{
			Name:    c.Name,
			Names:   c.Name, // Docker CLI compatibility
			Status:  c.Status,
			Project: c.Project,
			Agent:   c.Agent,
			Image:   truncateImage(c.Image),
			Created: formatCreatedTime(c.Created),
		})
	}
	return rows
}

func applyContainerFilters(containers []docker.Container, filters []cmdutil.Filter) []docker.Container {
	if len(filters) == 0 {
		return containers
	}
	var result []docker.Container
	for _, c := range containers {
		if matchesContainerFilters(c, filters) {
			result = append(result, c)
		}
	}
	return result
}

func matchesContainerFilters(c docker.Container, filters []cmdutil.Filter) bool {
	for _, f := range filters {
		switch f.Key {
		case "name":
			if !matchGlob(c.Name, f.Value) {
				return false
			}
		case "status":
			if !strings.EqualFold(c.Status, f.Value) {
				return false
			}
		case "agent":
			if !matchGlob(c.Agent, f.Value) {
				return false
			}
		}
	}
	return true
}

// matchGlob matches a string against a pattern with optional trailing wildcard.
func matchGlob(s, pattern string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(s, prefix)
	}
	return s == pattern
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
