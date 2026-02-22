// Package list provides the worktree list command.
package list

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// ListOptions contains the options for the list command.
type ListOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)

	All   bool
	Quiet bool
}

// NewCmdList creates the worktree list command.
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List worktrees for the current project",
		Long: `Lists git worktrees registered for the current project.

Shows the branch name, filesystem path, HEAD commit, and last modified time
for each worktree. Use --all to list worktrees across all registered projects.`,
		Example: `  # List worktrees for the current project
  clawker worktree list

  # List worktrees (short form)
  clawker worktree ls

  # List worktrees across all projects
  clawker worktree ls -a

  # List only branch names
  clawker worktree ls -q`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "List worktrees across all registered projects")
	cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "Only display branch names")

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	mgr, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	var states []project.WorktreeState
	if opts.All {
		states, err = mgr.ListWorktrees(ctx)
		if err != nil {
			return err
		}
	} else {
		proj, projErr := mgr.CurrentProject(ctx)
		if projErr != nil {
			if errors.Is(projErr, project.ErrNotInProjectPath) || errors.Is(projErr, project.ErrProjectNotRegistered) || errors.Is(projErr, project.ErrProjectNotFound) {
				return fmt.Errorf("not in a registered project directory")
			}
			return projErr
		}
		states, err = proj.ListWorktrees(ctx)
		if err != nil {
			return err
		}
	}

	if len(states) == 0 {
		if !opts.Quiet {
			if opts.All {
				fmt.Fprintln(opts.IOStreams.ErrOut, "No worktrees found across any registered projects.")
			} else {
				fmt.Fprintln(opts.IOStreams.ErrOut, "No worktrees found for this project.")
				fmt.Fprintln(opts.IOStreams.ErrOut, "Use `clawker worktree add --help` or create one automatically with `clawker run --worktree <branch>`.")
			}
		}
		return nil
	}

	// Quiet mode
	if opts.Quiet {
		for _, wt := range states {
			if opts.All {
				fmt.Fprintf(opts.IOStreams.Out, "%s\t%s\n", wt.Project, wt.Branch)
			} else {
				fmt.Fprintln(opts.IOStreams.Out, wt.Branch)
			}
		}
		return nil
	}

	// Full table
	w := tabwriter.NewWriter(opts.IOStreams.Out, 0, 4, 2, ' ', 0)
	if opts.All {
		fmt.Fprintln(w, "PROJECT\tBRANCH\tPATH\tHEAD\tMODIFIED\tSTATUS")
	} else {
		fmt.Fprintln(w, "BRANCH\tPATH\tHEAD\tMODIFIED\tSTATUS")
	}

	staleCount := 0
	for _, wt := range states {
		branch := wt.Branch
		if wt.IsDetached {
			branch = "(detached)"
		}

		modified := ""
		if wt.Path != "" {
			if info, statErr := os.Stat(wt.Path); statErr == nil {
				modified = formatTimeAgo(info.ModTime())
			}
		}

		status := string(wt.Status)
		if wt.InspectError != nil {
			status = fmt.Sprintf("error: %v", wt.InspectError)
			staleCount++
		} else if wt.Status != project.WorktreeHealthy {
			staleCount++
		}

		if opts.All {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", wt.Project, branch, wt.Path, wt.Head, modified, status)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", branch, wt.Path, wt.Head, modified, status)
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

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
