// Package prune provides the image prune command.
package prune

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the prune command.
type Options struct {
	Force bool
	All   bool
}

// NewCmd creates the image prune command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused images",
		Long: `Removes all unused clawker-managed images.

By default, only dangling images (untagged images) are removed.
Use --all to remove all images not used by any container.

Use with caution as this will permanently delete images.`,
		Example: `  # Remove unused (dangling) clawker images
  clawker image prune

  # Remove all unused clawker images
  clawker image prune --all

  # Remove without confirmation prompt
  clawker image prune --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, f, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Do not prompt for confirmation")
	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Remove all unused images, not just dangling ones")

	return cmd
}

func run(cmd *cobra.Command, _ *cmdutil.Factory, opts *Options) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Prompt for confirmation if not forced
	if !opts.Force {
		warning := "WARNING! This will remove all dangling clawker-managed images."
		if opts.All {
			warning = "WARNING! This will remove all unused clawker-managed images."
		}
		fmt.Fprint(os.Stderr, warning+"\nAre you sure you want to continue? [y/N] ")
		reader := bufio.NewReader(cmd.InOrStdin())
		response, err := reader.ReadString('\n')
		if err != nil {
			// Treat read errors (EOF, etc.) as "no"
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		response = strings.TrimSpace(response)
		if response != "y" && response != "Y" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// TODO: implement ImagesPrune in pkg/whail/image.go
	// For now, list and remove images one by one as a workaround
	listOpts := image.ListOptions{
		All: opts.All, // Include intermediate images if --all
	}
	images, err := client.ImageList(ctx, listOpts)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	if len(images) == 0 {
		fmt.Fprintln(os.Stderr, "No clawker images to remove.")
		return nil
	}

	// Filter to only dangling images if not --all
	var toRemove []image.Summary
	if opts.All {
		toRemove = images
	} else {
		for _, img := range images {
			// Dangling images have no tags
			if len(img.RepoTags) == 0 || (len(img.RepoTags) == 1 && img.RepoTags[0] == "<none>:<none>") {
				toRemove = append(toRemove, img)
			}
		}
	}

	if len(toRemove) == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker images to remove.")
		return nil
	}

	var removed int
	var reclaimedSpace int64
	removeOpts := image.RemoveOptions{
		Force:         false,
		PruneChildren: true,
	}

	for _, img := range toRemove {
		// Try to remove the image (will fail if in use)
		responses, err := client.ImageRemove(ctx, img.ID, removeOpts)
		if err != nil {
			// Check if it's an "in use" error vs unexpected error
			if strings.Contains(err.Error(), "image is being used") ||
				strings.Contains(err.Error(), "image has dependent child images") {
				continue
			}
			// Log unexpected errors but continue with other images
			fmt.Fprintf(os.Stderr, "Warning: failed to remove image %s: %v\n", truncateID(img.ID), err)
			continue
		}

		for _, resp := range responses {
			if resp.Untagged != "" {
				fmt.Fprintf(os.Stderr, "Untagged: %s\n", resp.Untagged)
			}
			if resp.Deleted != "" {
				fmt.Fprintf(os.Stderr, "Deleted: %s\n", resp.Deleted)
				removed++
			}
		}
		reclaimedSpace += img.Size
	}

	if removed == 0 {
		fmt.Fprintln(os.Stderr, "No unused clawker images to remove.")
	} else {
		fmt.Fprintf(os.Stderr, "\nTotal reclaimed space: %s\n", formatBytes(reclaimedSpace))
	}

	return nil
}

// truncateID shortens an image ID to 12 characters.
func truncateID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
