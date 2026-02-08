// Package list provides the image list command.
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

// Valid filter keys for image list.
var imageListValidFilterKeys = []string{"reference"}

// ListOptions holds options for the list command.
type ListOptions struct {
	IOStreams *iostreams.IOStreams
	TUI      *tui.TUI
	Client   func(context.Context) (*docker.Client, error)

	Format *cmdutil.FormatFlags
	Filter *cmdutil.FilterFlags
	All    bool
}

// NewCmdList creates the image list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams: f.IOStreams,
		TUI:      f.TUI,
		Client:   f.Client,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List images",
		Long: `Lists all images created by clawker.

Images are built from project configurations and can be shared
across multiple containers.`,
		Example: `  # List all clawker images
  clawker image list

  # List images (short form)
  clawker image ls

  # List image IDs only
  clawker image ls -q

  # Show all images (including intermediate)
  clawker image ls -a

  # Output as JSON
  clawker image ls --json

  # Custom Go template
  clawker image ls --format '{{.ID}} {{.Size}}'

  # Filter by reference
  clawker image ls --filter reference=myapp*`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	opts.Filter = cmdutil.AddFilterFlags(cmd)
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all images (default hides intermediate images)")

	return cmd
}

// imageRow is the data structure exposed to --format templates and --json output.
type imageRow struct {
	Image   string `json:"image"`
	ID      string `json:"id"`
	Created string `json:"created"`
	Size    string `json:"size"`
}

func listRun(ctx context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	filters, err := opts.Filter.Parse()
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateFilterKeys(filters, imageListValidFilterKeys); err != nil {
		return err
	}

	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	listOpts := docker.ImageListOptions{
		All: opts.All,
	}
	images, err := client.ImageList(ctx, listOpts)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	// Apply local filters.
	items := applyImageFilters(images.Items, filters)

	if len(items) == 0 {
		fmt.Fprintln(ios.ErrOut, "No clawker images found.")
		return nil
	}

	// Build display rows (one per repo tag, or one for untagged images).
	rows := buildImageRows(items)

	switch {
	case opts.Format.Quiet:
		for _, img := range items {
			fmt.Fprintln(ios.Out, truncateID(img.ID))
		}
		return nil

	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

	default:
		tp := opts.TUI.NewTable("IMAGE", "ID", "CREATED", "SIZE")
		for _, r := range rows {
			tp.AddRow(r.Image, r.ID, r.Created, r.Size)
		}
		return tp.Render()
	}
}

// buildImageRows converts ImageSummary items into display rows.
func buildImageRows(items []docker.ImageSummary) []imageRow {
	var rows []imageRow
	for _, img := range items {
		if len(img.RepoTags) == 0 {
			rows = append(rows, imageRow{
				Image:   "<none>:<none>",
				ID:      truncateID(img.ID),
				Created: formatCreated(img.Created),
				Size:    formatSize(img.Size),
			})
			continue
		}
		for _, tag := range img.RepoTags {
			repo, tagName := parseRepoTag(tag)
			rows = append(rows, imageRow{
				Image:   repo + ":" + tagName,
				ID:      truncateID(img.ID),
				Created: formatCreated(img.Created),
				Size:    formatSize(img.Size),
			})
		}
	}
	return rows
}

// applyImageFilters filters images locally based on --filter flags.
func applyImageFilters(items []docker.ImageSummary, filters []cmdutil.Filter) []docker.ImageSummary {
	if len(filters) == 0 {
		return items
	}
	var result []docker.ImageSummary
	for _, img := range items {
		if matchesImageFilters(img, filters) {
			result = append(result, img)
		}
	}
	return result
}

func matchesImageFilters(img docker.ImageSummary, filters []cmdutil.Filter) bool {
	for _, f := range filters {
		switch f.Key {
		case "reference":
			if !matchesReference(img, f.Value) {
				return false
			}
		}
	}
	return true
}

// matchesReference checks if any of the image's repo tags match a reference
// pattern. Supports trailing wildcard (e.g., "myapp*").
func matchesReference(img docker.ImageSummary, pattern string) bool {
	for _, tag := range img.RepoTags {
		if matchGlob(tag, pattern) {
			return true
		}
	}
	return false
}

// matchGlob does simple glob matching with trailing * only.
func matchGlob(s, pattern string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(s, prefix)
	}
	return s == pattern
}

// truncateID shortens an image ID to 12 characters.
func truncateID(id string) string {
	// Remove sha256: prefix if present
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// parseRepoTag splits a repository:tag string.
func parseRepoTag(repoTag string) (repo, tag string) {
	if before, after, ok := strings.Cut(repoTag, "@"); ok {
		return before, after
	}
	lastColon := strings.LastIndex(repoTag, ":")
	lastSlash := strings.LastIndex(repoTag, "/")
	if lastColon != -1 && lastColon > lastSlash {
		return repoTag[:lastColon], repoTag[lastColon+1:]
	}
	return repoTag, "<none>"
}

// formatCreated formats the creation timestamp into relative time.
func formatCreated(timestamp int64) string {
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
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case duration < 30*24*time.Hour:
		weeks := int(duration.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", weeks)
	case duration < 365*24*time.Hour:
		months := int(duration.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(duration.Hours() / 24 / 365)
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// formatSize formats the image size into human-readable format.
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2fGB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2fMB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2fKB", float64(size)/KB)
	default:
		return fmt.Sprintf("%dB", size)
	}
}
