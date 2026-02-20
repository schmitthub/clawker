// Package list provides the worktree list command.
package list

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/text"
	"github.com/spf13/cobra"
)

// ListOptions contains the options for the list command.
type ListOptions struct {
	IOStreams  *iostreams.IOStreams
	GitManager func() (*git.GitManager, error)
	ProjectManager func() (project.ProjectManager, error)

	Quiet bool
}

// NewCmdList creates the worktree list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:  f.IOStreams,
		GitManager: f.GitManager,
		ProjectManager: f.ProjectManager,
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
	projectManager, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	proj, err := projectManager.FromCWD(ctx)
	if err != nil {
		if errors.Is(err, project.ErrNotInProjectPath) || errors.Is(err, project.ErrProjectNotRegistered) || errors.Is(err, project.ErrProjectNotFound) {
			return fmt.Errorf("not in a registered project directory")
		}
		return err
	}

	projectSlug, projectEntry, err := proj.CurrentProject()
	if err != nil {
		return err
	}

	if len(projectEntry.Worktrees) == 0 {
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

	var entries []git.WorktreeDirEntry
	for name, worktree := range projectEntry.Worktrees {
		path := worktree.Path
		if path == "" {
			path = filepath.Join(config.ConfigDir(), "projects", projectSlug, text.Slugify(name))
		}
		entries = append(entries, git.WorktreeDirEntry{Name: name, Slug: text.Slugify(name), Path: path})
	}

	worktrees, err := gitMgr.ListWorktrees(entries)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	// Quiet mode - just branch names
	if opts.Quiet {
		for _, wt := range worktrees {
			fmt.Fprintln(opts.IOStreams.Out, wt.Name)
		}
		return nil
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

		status := "healthy"
		if wt.Error != nil {
			status = fmt.Sprintf("error: %v", wt.Error)
			staleCount++
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
