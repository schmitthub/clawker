// Package show provides the project show command.
package show

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

// ShowOptions contains the options for the project show command.
type ShowOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)
	Format         *cmdutil.FormatFlags

	Name string
}

// projectDetail is the display/serialisation shape for project details.
type projectDetail struct {
	Name      string           `json:"name"`
	Root      string           `json:"root"`
	Exists    bool             `json:"exists"`
	Worktrees []worktreeDetail `json:"worktrees"`
}

type worktreeDetail struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// NewCmdShow creates the project show command.
func NewCmdShow(f *cmdutil.Factory, runF func(context.Context, *ShowOptions) error) *cobra.Command {
	opts := &ShowOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show details of a registered project",
		Long: `Shows detailed information about a registered project, including its
name, root path, directory status, and any registered worktrees.`,
		Example: `  # Show project details
  clawker project show my-app

  # Output as JSON
  clawker project show my-app --json`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return showRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)

	return cmd
}

func showRun(ctx context.Context, opts *ShowOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	mgr, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	entry, err := findProjectByName(ctx, mgr, opts.Name)
	if err != nil {
		return err
	}

	detail := buildProjectDetail(entry)

	if opts.Format.IsJSON() {
		return cmdutil.WriteJSON(ios.Out, detail)
	}

	// Key-value display.
	status := cs.Success("ok")
	if !detail.Exists {
		status = cs.Warning("missing")
	}

	fmt.Fprintf(ios.Out, "Name:       %s\n", detail.Name)
	fmt.Fprintf(ios.Out, "Root:       %s\n", detail.Root)
	fmt.Fprintf(ios.Out, "Status:     %s\n", status)

	if len(detail.Worktrees) > 0 {
		fmt.Fprintf(ios.Out, "Worktrees:  %d\n", len(detail.Worktrees))
		for _, wt := range detail.Worktrees {
			fmt.Fprintf(ios.Out, "  %s  %s\n", wt.Branch, wt.Path)
		}
	} else {
		fmt.Fprintf(ios.Out, "Worktrees:  none\n")
	}

	return nil
}

func findProjectByName(ctx context.Context, mgr project.ProjectManager, name string) (config.ProjectEntry, error) {
	entries, err := mgr.List(ctx)
	if err != nil {
		return config.ProjectEntry{}, fmt.Errorf("listing projects: %w", err)
	}
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return config.ProjectEntry{}, fmt.Errorf("project %q is not registered", name)
}

func buildProjectDetail(entry config.ProjectEntry) projectDetail {
	detail := projectDetail{
		Name:   entry.Name,
		Root:   entry.Root,
		Exists: dirExists(entry.Root),
	}

	if len(entry.Worktrees) > 0 {
		for _, wt := range entry.Worktrees {
			detail.Worktrees = append(detail.Worktrees, worktreeDetail{
				Branch: wt.Branch,
				Path:   wt.Path,
			})
		}
		sort.Slice(detail.Worktrees, func(i, j int) bool {
			return detail.Worktrees[i].Branch < detail.Worktrees[j].Branch
		})
	}

	return detail
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
