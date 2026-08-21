package sockets

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// ListOptions holds dependencies for the socket grant list command.
type ListOptions struct {
	IOStreams    *iostreams.IOStreams
	TUI          *tui.TUI
	SocketGrants socketGrantStoreFunc
	Format       *cmdutil.FormatFlags
}

// listRow is the machine-output shape for one socket grant summary.
type listRow struct {
	ID      int64  `json:"id"`
	Harness string `json:"harness"`
	Status  string `json:"status"`
	Purpose string `json:"purpose"`
}

func newCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IOStreams:    f.IOStreams,
		TUI:          f.TUI,
		SocketGrants: socketGrants(f),
		Format:       nil,
	}
	cmd := &cobra.Command{ //nolint:exhaustruct_v5 // Cobra command fields use their documented zero-value defaults.
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored socket bridge grants",
		Example: `  # List all approvals and denials
  clawker sockets list

  # Output summaries as JSON
  clawker sockets list --json`,
		Args: cmdutil.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return listRun(cmd.Context(), opts)
		},
	}
	opts.Format = cmdutil.AddFormatFlags(cmd)
	return cmd
}

func listRun(_ context.Context, opts *ListOptions) error {
	store, err := opts.SocketGrants()
	if err != nil {
		return fmt.Errorf("list socket grants: open database: %w", err)
	}
	grants, err := store.ListSocketGrants()
	if err != nil {
		return fmt.Errorf("list socket grants: %w", err)
	}
	rows := make([]listRow, 0, len(grants))
	for _, grant := range grants {
		rows = append(rows, listRow{
			ID: grant.ID, Harness: grant.HarnessName, Status: grant.Status, Purpose: grant.Purpose,
		})
	}
	return renderGrantList(opts, rows)
}

func renderGrantList(opts *ListOptions, rows []listRow) error {
	if opts.Format.Quiet {
		return renderGrantIDs(opts, rows)
	}
	if opts.Format.IsJSON() {
		if err := cmdutil.WriteJSON(opts.IOStreams.Out, rows); err != nil {
			return fmt.Errorf("write socket grants as JSON: %w", err)
		}
		return nil
	}
	if opts.Format.IsTemplate() {
		items := make([]any, len(rows))
		for i := range rows {
			items[i] = rows[i]
		}
		if err := cmdutil.ExecuteTemplate(opts.IOStreams.Out, opts.Format.Template(), items); err != nil {
			return fmt.Errorf("write socket grants from template: %w", err)
		}
		return nil
	}
	table := opts.TUI.NewTable("ID", "HARNESS", "STATUS", "PURPOSE")
	for _, row := range rows {
		table.AddRow(strconv.FormatInt(row.ID, 10), row.Harness, row.Status, row.Purpose)
	}
	if err := table.Render(); err != nil {
		return fmt.Errorf("render socket grants: %w", err)
	}
	return nil
}

func renderGrantIDs(opts *ListOptions, rows []listRow) error {
	for _, row := range rows {
		if _, err := fmt.Fprintln(opts.IOStreams.Out, row.ID); err != nil {
			return fmt.Errorf("write socket grant ID: %w", err)
		}
	}
	return nil
}
