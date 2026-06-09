package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
)

// AgentsOptions wires the command's run function. The agents read path
// goes through the AdminService gRPC surface — CP is the SOLE writer
// of the agent registry, so the host can no longer read sqlite
// directly. `f.AdminClient(ctx).ListAgents` is the canonical access.
type AgentsOptions struct {
	IOStreams   *iostreams.IOStreams
	TUI         *tui.TUI
	Logger      func() (*logger.Logger, error)
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Format      *cmdutil.FormatFlags
}

// agentRow is the JSON/template-friendly representation of one agent.
// Field tags are the wire contract for `--json` consumers — a rename
// here breaks downstream tooling.
type agentRow struct {
	AgentName      string `json:"agent_name"`
	Project        string `json:"project"`
	ContainerID    string `json:"container_id"`
	CertThumbprint string `json:"cert_thumbprint"`
	RegisteredAt   string `json:"registered_at"`
	LastSeen       string `json:"last_seen"`
}

// NewCmdAgents creates the `clawker controlplane agents` command.
func NewCmdAgents(f *cmdutil.Factory, runF func(context.Context, *AgentsOptions) error) *cobra.Command {
	opts := &AgentsOptions{
		IOStreams:   f.IOStreams,
		TUI:         f.TUI,
		Logger:      f.Logger,
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List agents currently registered with the control plane",
		Long: `List every agent currently registered with the control plane.

The thumbprint shown is the SHA-256 of the agent's certificate. Agents
are uniquely identified by the (project, agent_name) pair — agents with
the same name in different projects appear as separate rows.`,
		Example: `  # Show all registered agents
  clawker controlplane agents

  # Machine-readable output
  clawker controlplane agents --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return agentsRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)
	return cmd
}

func agentsRun(ctx context.Context, opts *AgentsOptions) error {
	client, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("dialing control plane: %w", err)
	}

	resp, err := client.ListAgents(ctx, &adminv1.ListAgentsRequest{})
	if err != nil {
		return fmt.Errorf("ListAgents: %w", err)
	}

	rows := make([]agentRow, len(resp.GetAgents()))
	for i, a := range resp.GetAgents() {
		rows[i] = agentRow{
			AgentName:      a.GetAgentName(),
			Project:        a.GetProject(),
			ContainerID:    a.GetContainerId(),
			CertThumbprint: a.GetCertThumbprint(),
			RegisteredAt:   formatUnix(a.GetRegisteredAtUnix()),
			LastSeen:       formatUnix(a.GetLastSeenUnix()),
		}
	}
	return renderAgents(opts, rows)
}

func renderAgents(opts *AgentsOptions, rows []agentRow) error {
	ios := opts.IOStreams

	switch {
	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, rows)
	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))
	}

	if len(rows) == 0 {
		cs := ios.ColorScheme()
		fmt.Fprintf(ios.ErrOut, "%s No agents registered\n", cs.InfoIcon())
		return nil
	}

	table := opts.TUI.NewTable("AGENT", "PROJECT", "CONTAINER", "THUMBPRINT", "REGISTERED", "LAST SEEN")
	for _, r := range rows {
		table.AddRow(
			r.AgentName,
			r.Project,
			short(r.ContainerID, 12),
			short(r.CertThumbprint, 12),
			r.RegisteredAt,
			r.LastSeen,
		)
	}
	return table.Render()
}

func formatUnix(unix int64) string {
	// RegisteredAt / LastSeen are written by CP at Register handler
	// entry with time.Now() and should never be zero on a healthy row.
	// Render zero as a loud sentinel so registry corruption surfaces
	// in the table instead of being silently confused with "looks fine".
	if unix == 0 {
		return "<unset>"
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
