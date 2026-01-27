// Package list provides the image list command.
package list

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	cmdutil2 "github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// Options holds options for the list command.
type Options struct {
	Quiet bool
	All   bool
}

// NewCmd creates the image list command.
func NewCmd(f *cmdutil2.Factory) *cobra.Command {
	opts := &Options{}

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
  clawker image ls -a`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display image IDs")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all images (default hides intermediate images)")

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

	// List images
	listOpts := docker.ImageListOptions{
		All: opts.All,
	}
	images, err := client.ImageList(ctx, listOpts)
	if err != nil {
		cmdutil2.HandleError(ios, err)
		return err
	}

	if len(images.Items) == 0 {
		fmt.Fprintln(ios.ErrOut, "No clawker images found.")
		return nil
	}

	// Quiet mode - just print IDs
	if opts.Quiet {
		for _, img := range images.Items {
			fmt.Fprintln(ios.Out, truncateID(img.ID))
		}
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE\tID\tCREATED\tSIZE")

	for _, img := range images.Items {
		// Handle images with multiple tags or no tags
		if len(img.RepoTags) == 0 {
			fmt.Fprintf(w, "%s:%s\t%s\t%s\t%s\n",
				"<none>",
				"<none>",
				truncateID(img.ID),
				formatCreated(img.Created),
				formatSize(img.Size),
			)
			continue
		}

		for _, tag := range img.RepoTags {
			repo, tagName := parseRepoTag(tag)
			fmt.Fprintf(w, "%s:%s\t%s\t%s\t%s\n",
				repo,
				tagName,
				truncateID(img.ID),
				formatCreated(img.Created),
				formatSize(img.Size),
			)
		}
	}

	return w.Flush()
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
	if at := strings.Index(repoTag, "@"); at != -1 {
		return repoTag[:at], repoTag[at+1:]
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
