package controlplane

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
)

// AgentsOptions wires the command's run function. The agents read path
// is local-only — the CLI is the sole writer of the agentregistry
// sqlite DB and reads it directly off the host filesystem rather than
// going through CP gRPC. This keeps `clawker controlplane agents`
// working when the CP is down (the data point is still there) and
// avoids paying a dial+RPC cost to read what we just wrote.
//
// The registry path is intentionally NOT a field here. It comes from
// `consts.ControlPlaneDBPath()`, which reads `CLAWKER_DATA_DIR` at call
// time; tests rely on `testenv.New(t)` to set that env var to an
// isolated temp dir, so the accessor resolves correctly without the
// command needing a Factory noun for what is really just a consts
// lookup.
type AgentsOptions struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Logger    func() (*logger.Logger, error)
	Format    *cmdutil.FormatFlags
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
		IOStreams: f.IOStreams,
		TUI:       f.TUI,
		Logger:    f.Logger,
	}

	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List agents currently registered with the control plane",
		Long: `Snapshot every agent the CLI has registered with the control plane.

The CLI is the sole writer of the agent registry — entries are written
at container creation time alongside auth material delivery. This
command reads the registry sqlite database directly off the host
filesystem and works whether or not the control plane is running.

Identity is channel-bound: the certificate thumbprint shown here is the
SHA-256 over the agent's mTLS leaf cert. Agents are uniquely identified
by the composite (project, agent_name) — agents with the same short
name in different projects appear as separate rows.`,
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

func agentsRun(_ context.Context, opts *AgentsOptions) error {
	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	dbPath, err := consts.ControlPlaneDBPath()
	if err != nil {
		return fmt.Errorf("resolving registry db path: %w", err)
	}

	rows, err := loadAgentRows(dbPath, log)
	if err != nil {
		return err
	}
	return renderAgents(opts, rows)
}

// loadAgentRows opens the registry in read-only mode, snapshots every
// entry, and converts to the wire row shape. A missing DB file means
// no `clawker run` has ever created a container on this host — return
// an empty list rather than an error so the empty-state branch in the
// caller renders normally.
func loadAgentRows(dbPath string, log *logger.Logger) ([]agentRow, error) {
	reg, err := agentregistry.NewSQLiteReader(dbPath, log)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening registry: %w", err)
	}
	defer func() {
		closer, ok := reg.(interface{ Close() error })
		if !ok {
			return
		}
		if err := closer.Close(); err != nil {
			// Best-effort: log the error if we can. CLI exit-time
			// path; logger is still alive. If logger init itself
			// fails (extremely unlikely here — same logger was just
			// resolved successfully a few lines up) silently drop.
			log.Debug().Err(err).Msg("agents: sqlite reader close failed")
		}
	}()

	snap := reg.Snapshot()
	rows := make([]agentRow, len(snap))
	for i, e := range snap {
		rows[i] = agentRow{
			AgentName:      e.AgentName,
			Project:        e.Project,
			ContainerID:    e.ContainerID,
			CertThumbprint: hex.EncodeToString(e.Thumbprint[:]),
			RegisteredAt:   formatUnix(e.RegisteredAt.Unix()),
			LastSeen:       formatUnix(e.LastSeen.Unix()),
		}
	}
	return rows, nil
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
