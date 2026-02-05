// Package list provides the worktree list command.
package list

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// ListOptions contains the options for the list command.
type ListOptions struct {
	IOStreams  *iostreams.IOStreams
	GitManager func() (*git.GitManager, error)
	Config     func() *config.Config

	Quiet bool
}

// NewCmdList creates the worktree list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:  f.IOStreams,
		GitManager: f.GitManager,
		Config:     f.Config,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List worktrees for the current project",
		Long: `Lists all git worktrees registered for the current project.

Shows the branch name, filesystem path, HEAD commit, and last modified time
for each worktree.`,
		Example: `  # List all worktrees
  clawker worktree list

  # List worktrees (short form)
  clawker worktree ls

  # List only branch names
  clawker worktree ls -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display branch names")

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	cfg := opts.Config()

	// Check if we're in a registered project
	if !cfg.Project.Found() {
		return fmt.Errorf("not in a registered project directory")
	}

	// Get worktree handles from registry
	var worktreeHandles []config.WorktreeHandle
	if cfg.Registry != nil {
		projectHandle := cfg.Registry.Project(cfg.Project.Key())
		handles, err := projectHandle.ListWorktrees()
		if err != nil {
			return fmt.Errorf("listing worktrees from registry: %w", err)
		}
		worktreeHandles = handles
	}

	if len(worktreeHandles) == 0 {
		if !opts.Quiet {
			fmt.Fprintln(opts.IOStreams.ErrOut, "No worktrees found for this project.")
			fmt.Fprintln(opts.IOStreams.ErrOut, "Use 'clawker run --worktree <branch>' to create one.")
		}
		return nil
	}

	// Get git manager for detailed worktree info (HEAD, branch, etc.)
	gitMgr, err := opts.GitManager()
	if err != nil {
		return fmt.Errorf("initializing git: %w", err)
	}

	// Separate entries with valid paths from those with path errors
	var validEntries []git.WorktreeDirEntry
	var errorWorktrees []git.WorktreeInfo

	for _, handle := range worktreeHandles {
		status := handle.Status()
		if status.Error != nil {
			// Path resolution failed - skip git lookup, record error for display
			logger.Debug().Err(status.Error).Str("worktree", handle.Name()).Msg("failed to resolve worktree path")
			errorWorktrees = append(errorWorktrees, git.WorktreeInfo{
				Name:   handle.Name(),
				Slug:   handle.Slug(),
				Branch: handle.Name(), // Set Branch for display in table
				Path:   "",            // No valid path
				Error:  fmt.Errorf("path error: %w", status.Error),
			})
			continue
		}
		validEntries = append(validEntries, git.WorktreeDirEntry{
			Name: handle.Name(),
			Slug: handle.Slug(),
			Path: status.Path,
		})
	}

	// List worktrees with git metadata (only for entries with valid paths)
	worktrees, err := gitMgr.ListWorktrees(validEntries)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	// Append error entries to the list
	worktrees = append(worktrees, errorWorktrees...)

	// Quiet mode - just branch names
	if opts.Quiet {
		for _, wt := range worktrees {
			fmt.Fprintln(opts.IOStreams.Out, wt.Name)
		}
		return nil
	}

	// Build a map of slug -> handle for status checks
	handleBySlug := make(map[string]config.WorktreeHandle, len(worktreeHandles))
	for _, h := range worktreeHandles {
		handleBySlug[h.Slug()] = h
	}

	// Print table
	w := tabwriter.NewWriter(opts.IOStreams.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tPATH\tHEAD\tMODIFIED\tSTATUS")

	staleCount := 0
	for _, wt := range worktrees {
		// Get branch display (or "(detached)" for detached HEAD)
		branch := wt.Branch
		if wt.IsDetached {
			branch = "(detached)"
		}

		// Get HEAD short hash
		head := ""
		if !wt.Head.IsZero() {
			head = wt.Head.String()[:7]
		}

		// Get last modified time from path
		modified := ""
		if wt.Path != "" {
			if info, statErr := os.Stat(wt.Path); statErr == nil {
				modified = formatTimeAgo(info.ModTime())
			} else if !os.IsNotExist(statErr) {
				// Surface non-existence errors (e.g., permission issues) to the user
				// by aggregating them into the error field
				if wt.Error != nil {
					wt.Error = fmt.Errorf("%v; stat error: %w", wt.Error, statErr)
				} else {
					wt.Error = fmt.Errorf("stat error: %w", statErr)
				}
			}
		}

		// Determine status using handle-based health check
		status := ""
		if handle, ok := handleBySlug[wt.Slug]; ok {
			wtStatus := handle.Status()
			// Skip handle status for path errors - use wt.Error instead (has better context)
			if wtStatus.Error == nil && !wtStatus.IsHealthy() {
				status = wtStatus.String()
				if wtStatus.IsPrunable() {
					staleCount++
				}
			}
		}
		// Fall back to error from wt.Error if we didn't get a handle status
		if status == "" && wt.Error != nil {
			status = fmt.Sprintf("error: %v", wt.Error)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", branch, wt.Path, head, modified, status)
	}

	if err := w.Flush(); err != nil {
		return err
	}

	// Show prune warning if there are stale entries
	if staleCount > 0 {
		fmt.Fprintln(opts.IOStreams.ErrOut)
		if staleCount == 1 {
			fmt.Fprintln(opts.IOStreams.ErrOut, "Warning: 1 stale entry detected. Run `clawker worktree prune` to clean up.")
		} else {
			fmt.Fprintf(opts.IOStreams.ErrOut, "Warning: %d stale entries detected. Run `clawker worktree prune` to clean up.\n", staleCount)
		}
	}

	return nil
}

// formatTimeAgo returns a human-readable relative time string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
