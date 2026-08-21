package sockets

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// InfoOptions holds dependencies for the socket grant info command.
type InfoOptions struct {
	IOStreams    *iostreams.IOStreams
	TUI          *tui.TUI
	SocketGrants socketGrantStoreFunc
	Format       *cmdutil.FormatFlags
	ID           int64
}

// infoRow is the full machine-output shape for one socket grant.
//
//nolint:tagliatelle // snake_case is the command JSON contract
type infoRow struct {
	ID            int64  `json:"id"`
	HarnessName   string `json:"harness_name"`
	HarnessPath   string `json:"harness_path"`
	HostPath      string `json:"host_path"`
	Status        string `json:"status"`
	ListenerUID   int    `json:"listener_uid"`
	ListenerGID   int    `json:"listener_gid"`
	ListenerOwner string `json:"listener_owner"`
	ListenerGroup string `json:"listener_group"`
	Purpose       string `json:"purpose"`
	GrantedAt     string `json:"granted_at"`
}

func newCmdInfo(f *cmdutil.Factory, runF func(context.Context, *InfoOptions) error) *cobra.Command {
	opts := &InfoOptions{
		IOStreams:    f.IOStreams,
		TUI:          f.TUI,
		SocketGrants: socketGrants(f),
		Format:       nil,
		ID:           0,
	}
	cmd := &cobra.Command{ //nolint:exhaustruct_v5 // Cobra command fields use their documented zero-value defaults.
		Use:   "info <id>",
		Short: "Show one socket bridge grant",
		Example: `  # Show all stored fields for grant 7
  clawker sockets info 7

  # Output the grant as JSON
  clawker sockets info 7 --json`,
		Args: cmdutil.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseGrantID(args[0])
			if err != nil {
				return err
			}
			opts.ID = id
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return infoRun(cmd.Context(), opts)
		},
	}
	cmd.ValidArgsFunction = grantIDCompletions(opts.SocketGrants)
	opts.Format = cmdutil.AddFormatFlags(cmd)
	return cmd
}

func infoRun(_ context.Context, opts *InfoOptions) error {
	store, err := opts.SocketGrants()
	if err != nil {
		return fmt.Errorf("get socket grant %d: open database: %w", opts.ID, err)
	}
	grants, err := store.ListSocketGrants()
	if err != nil {
		return fmt.Errorf("get socket grant %d: %w", opts.ID, err)
	}
	for _, grant := range grants {
		if grant.ID == opts.ID {
			return renderGrantInfo(opts, infoRow{
				ID:            grant.ID,
				HarnessName:   grant.HarnessName,
				HarnessPath:   grant.HarnessPath,
				HostPath:      grant.HostPath,
				Status:        grant.Status,
				ListenerUID:   grant.Identity.UID,
				ListenerGID:   grant.Identity.GID,
				ListenerOwner: grant.Identity.Owner,
				ListenerGroup: grant.Identity.Group,
				Purpose:       grant.Purpose,
				GrantedAt:     grant.GrantedAt.Format(time.RFC3339),
			})
		}
	}
	return fmt.Errorf("socket grant %d was not found", opts.ID)
}

func renderGrantInfo(opts *InfoOptions, row infoRow) error {
	if opts.Format.Quiet {
		if _, err := fmt.Fprintln(opts.IOStreams.Out, row.ID); err != nil {
			return fmt.Errorf("write socket grant ID: %w", err)
		}
		return nil
	}
	if opts.Format.IsJSON() {
		if err := cmdutil.WriteJSON(opts.IOStreams.Out, row); err != nil {
			return fmt.Errorf("write socket grant as JSON: %w", err)
		}
		return nil
	}
	if opts.Format.IsTemplate() {
		if err := cmdutil.ExecuteTemplate(opts.IOStreams.Out, opts.Format.Template(), []any{row}); err != nil {
			return fmt.Errorf("write socket grant from template: %w", err)
		}
		return nil
	}
	listener := fmt.Sprintf(
		"%s:%s (uid %d, gid %d)", row.ListenerOwner, row.ListenerGroup, row.ListenerUID, row.ListenerGID,
	)
	details := opts.TUI.RenderDetails([]tui.KeyValuePair{
		{Key: "ID", Value: strconv.FormatInt(row.ID, 10)},
		{Key: "Harness", Value: row.HarnessName},
		{Key: "Principal", Value: row.HarnessPath},
		{Key: "Host socket", Value: row.HostPath},
		{Key: "Status", Value: row.Status},
		{Key: "Listener", Value: listener},
		{Key: "Purpose", Value: row.Purpose},
		{Key: "Granted at", Value: row.GrantedAt},
	})
	if _, err := fmt.Fprintln(opts.IOStreams.Out, details); err != nil {
		return fmt.Errorf("write socket grant details: %w", err)
	}
	return nil
}

func parseGrantID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf(
			"parse socket grant ID: %w",
			cmdutil.FlagErrorf("socket grant ID must be a positive integer: %q", value),
		)
	}
	return id, nil
}
