package list

import (
	"context"
	"fmt"
	"strconv"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

type ListOptions struct {
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	ProjectManager func() (project.ProjectManager, error)
	Format         *cmdutil.FormatFlags
}

type projectRow struct {
	Name      string `json:"name"`
	Root      string `json:"root"`
	Worktrees int    `json:"worktrees"`
	Status    string `json:"status"`
}

func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:      f.IOStreams,
		TUI:            f.TUI,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered projects",
		Long: `Lists all projects registered in the clawker project registry.

Shows the project name, root path, number of worktrees, and directory
health status.`,
		Example: `  # List all registered projects
  clawker project list

  # List projects (short form)
  clawker project ls

  # List project names only
  clawker project list -q

  # Output as JSON
  clawker project list --json

  # Custom Go template
  clawker project list --format '{{.Name}} {{.Root}}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	cmd.Flags().Lookup("quiet").Usage = "Only display project names"

	return cmd
}

func listRun(ctx context.Context, opts *ListOptions) error {
	ios := opts.IOStreams

	mgr, err := opts.ProjectManager()
	if err != nil {
		return fmt.Errorf("loading project manager: %w", err)
	}

	states, err := mgr.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}

	if len(states) == 0 {
		fmt.Fprintln(ios.ErrOut, "No registered projects found.")
		fmt.Fprintln(ios.ErrOut, "Use 'clawker project init' to create a project or 'clawker project register' to register an existing one.")
		return nil
	}

	rows := buildProjectRows(states)

	switch {
	case opts.Format.Quiet:
		for _, r := range rows {
			fmt.Fprintln(ios.Out, r.Name)
		}
		return nil

	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

	default:
		tp := opts.TUI.NewTable("NAME", "ROOT", "WORKTREES", "STATUS")
		cs := ios.ColorScheme()
		for _, r := range rows {
			status := cs.Success(r.Status)
			if r.Status != string(project.ProjectOK) {
				status = cs.Warning(r.Status)
			}
			tp.AddRow(r.Name, r.Root, strconv.Itoa(r.Worktrees), status)
		}
		return tp.Render()
	}
}

func buildProjectRows(states []project.ProjectState) []projectRow {
	rows := make([]projectRow, 0, len(states))
	for _, s := range states {
		rows = append(rows, projectRow{
			Name:      s.Name,
			Root:      s.Root,
			Worktrees: len(s.Worktrees),
			Status:    string(s.Status),
		})
	}
	return rows
}
