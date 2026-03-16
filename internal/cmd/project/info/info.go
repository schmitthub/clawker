package info

import (
	"context"
	"fmt"
	"sort"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/spf13/cobra"
)

type InfoOptions struct {
	IOStreams      *iostreams.IOStreams
	ProjectManager func() (project.ProjectManager, error)

	Name string
	JSON bool
}

type projectDetail struct {
	Name      string           `json:"name"`
	Root      string           `json:"root"`
	Status    string           `json:"status"`
	Worktrees []worktreeDetail `json:"worktrees"`
}

type worktreeDetail struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

func NewCmdInfo(f *cmdutil.Factory, runF func(context.Context, *InfoOptions) error) *cobra.Command {
	opts := &InfoOptions{
		IOStreams:      f.IOStreams,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "info NAME",
		Short: "Show details of a registered project",
		Long: `Shows detailed information about a registered project, including its
name, root path, directory status, and any registered worktrees with
their health status.`,
		Example: `  # Show project details
  clawker project info my-app

  # Output as JSON
  clawker project info my-app --json`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return infoRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output as JSON")

	return cmd
}

func infoRun(ctx context.Context, opts *InfoOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	mgr, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	states, err := mgr.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}

	state, ok := findByName(states, opts.Name)
	if !ok {
		return fmt.Errorf("project %q is not registered; use 'clawker project list' to see registered projects", opts.Name)
	}

	detail := buildDetail(state)

	if opts.JSON {
		return cmdutil.WriteJSON(ios.Out, detail)
	}

	// Key-value display.
	status := cs.Success(detail.Status)
	if detail.Status != string(project.ProjectOK) {
		status = cs.Warning(detail.Status)
	}

	fmt.Fprintf(ios.Out, "Name:       %s\n", detail.Name)
	fmt.Fprintf(ios.Out, "Root:       %s\n", detail.Root)
	fmt.Fprintf(ios.Out, "Status:     %s\n", status)

	if len(detail.Worktrees) > 0 {
		fmt.Fprintf(ios.Out, "Worktrees:  %d\n", len(detail.Worktrees))
		for _, wt := range detail.Worktrees {
			fmt.Fprintf(ios.Out, "  %s  %s  (%s)\n", wt.Branch, wt.Path, wt.Status)
		}
	} else {
		fmt.Fprintf(ios.Out, "Worktrees:  none\n")
	}

	return nil
}

func findByName(states []project.ProjectState, name string) (project.ProjectState, bool) {
	for _, s := range states {
		if s.Name == name {
			return s, true
		}
	}
	return project.ProjectState{}, false
}

func buildDetail(state project.ProjectState) projectDetail {
	detail := projectDetail{
		Name:   state.Name,
		Root:   state.Root,
		Status: string(state.Status),
	}

	wts := make([]worktreeDetail, 0, len(state.Worktrees))
	for _, wt := range state.Worktrees {
		wts = append(wts, worktreeDetail{
			Branch: wt.Branch,
			Path:   wt.Path,
			Status: string(wt.Status),
		})
	}
	sort.Slice(wts, func(i, j int) bool {
		return wts[i].Branch < wts[j].Branch
	})
	detail.Worktrees = wts

	return detail
}
