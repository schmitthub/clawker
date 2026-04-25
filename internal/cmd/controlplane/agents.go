package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

type AgentsOptions struct {
	IOStreams   *iostreams.IOStreams
	TUI         *tui.TUI
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Format      *cmdutil.FormatFlags
}

// agentRow is the JSON/template-friendly representation of one agent.
// Field tags are the wire contract for `--json` consumers — a rename
// here breaks downstream tooling.
type agentRow struct {
	AgentName      string `json:"agent_name"`
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
		AdminClient: f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List agents currently registered with the control plane",
		Long: `Snapshot every agent that has completed AgentService.Register.

Identity is channel-bound: the certificate thumbprint shown here is the
SHA-256 over the agent's mTLS leaf cert and is what the control plane
uses as the registry key.`,
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
		return fmt.Errorf("connecting to control plane: %w", err)
	}
	resp, err := client.ListAgents(ctx, &adminv1.ListAgentsRequest{})
	if err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}

	rows := make([]agentRow, len(resp.Agents))
	for i, a := range resp.Agents {
		rows[i] = agentRow{
			AgentName:      a.AgentName,
			ContainerID:    a.ContainerId,
			CertThumbprint: a.CertThumbprint,
			RegisteredAt:   formatUnix(a.RegisteredAtUnix),
			LastSeen:       formatUnix(a.LastSeenUnix),
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

	table := opts.TUI.NewTable("AGENT", "CONTAINER", "THUMBPRINT", "REGISTERED", "LAST SEEN")
	for _, r := range rows {
		table.AddRow(
			r.AgentName,
			short(r.ContainerID, 12),
			short(r.CertThumbprint, 12),
			r.RegisteredAt,
			r.LastSeen,
		)
	}
	return table.Render()
}

func formatUnix(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
