package controlplane

import (
	"context"
	"fmt"
	"net/http"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

type StatusOptions struct {
	IOStreams    *iostreams.IOStreams
	ControlPlane func() cpboot.Manager
	AdminClient  func(context.Context) (adminv1.AdminServiceClient, error)
	Format       *cmdutil.FormatFlags
}

// statusRow is the JSON/template-friendly representation of CP status.
// Field names + json tags are a contract with the E2E test's
// cpStatusRow (test/e2e/controlplane_cli_test.go) — a rename here
// silently breaks JSON unmarshaling on the other side.
type statusRow struct {
	ContainerRunning bool   `json:"container_running"`
	HealthzOK        bool   `json:"healthz_ok"`
	HealthzStatus    int    `json:"healthz_status,omitempty"`
	HealthzError     string `json:"healthz_error,omitempty"`
	FirewallRunning  bool   `json:"firewall_running"`
	FirewallReady    bool   `json:"firewall_ready"`
	FirewallRuleCnt  int    `json:"firewall_rule_count"`
	FirewallError    string `json:"firewall_error,omitempty"`
}

// NewCmdStatus creates the controlplane status command. Tolerates an
// absent CP without bootstrapping — when IsRunning returns false the
// run function short-circuits before touching f.AdminClient (which
// would attempt a dial to a stopped CP and fail fast).
func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{
		IOStreams:    f.IOStreams,
		ControlPlane: f.ControlPlane,
		AdminClient:  f.AdminClient,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show control plane health",
		Long: `Report the health of the clawker control plane.

Probes the control plane's health endpoint and, if the CP is up,
reports firewall subsystem state. Tolerates a stopped CP — in that case
the firewall fields are omitted and the CP is reported as down.`,
		Example: `  # Show CP status
  clawker controlplane status

  # Output as JSON
  clawker controlplane status --json

  # Custom Go template
  clawker controlplane status --format '{{.ContainerRunning}}'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return statusRun(cmd.Context(), opts)
		},
	}

	opts.Format = cmdutil.AddFormatFlags(cmd)

	return cmd
}

func statusRun(ctx context.Context, opts *StatusOptions) error {
	mgr := opts.ControlPlane()

	var row statusRow
	running, err := mgr.IsRunning(ctx)
	if err != nil {
		return fmt.Errorf("checking control plane: %w", err)
	}
	row.ContainerRunning = running
	if !running {
		return renderStatus(opts, row)
	}

	status, probeErr := mgr.ProbeHealthz(ctx)
	row.HealthzStatus = status
	row.HealthzOK = status == http.StatusOK
	if probeErr != nil {
		row.HealthzError = probeErr.Error()
	}

	// Best-effort firewall snapshot. Both the AdminClient dial and the
	// RPC itself can fail while the CP is otherwise healthy (transient
	// restart, AdminService still initializing). Surface the error on
	// FirewallError rather than failing the command — `status` is a
	// diagnostic tool, not a gate.
	adminClient, dialErr := opts.AdminClient(ctx)
	switch {
	case dialErr != nil:
		row.FirewallError = dialErr.Error()
	default:
		resp, rpcErr := adminClient.FirewallStatus(ctx, &adminv1.FirewallStatusRequest{})
		if rpcErr != nil {
			row.FirewallError = rpcErr.Error()
		} else {
			row.FirewallRunning = resp.GetRunning()
			row.FirewallReady = resp.GetEnvoyHealth() && resp.GetCorednsHealth()
			row.FirewallRuleCnt = int(resp.GetRuleCount())
		}
	}

	return renderStatus(opts, row)
}

func renderStatus(opts *StatusOptions, row statusRow) error {
	ios := opts.IOStreams

	switch {
	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, row)
	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny([]statusRow{row}))
	}

	cs := ios.ColorScheme()
	boolIcon := func(ok bool) string {
		if ok {
			return cs.SuccessIcon()
		}
		return cs.FailureIcon()
	}

	containerText := cs.Error("stopped")
	if row.ContainerRunning {
		containerText = cs.Success("running")
	}

	fmt.Fprintf(ios.Out, "Container:  %s\n", containerText)
	if !row.ContainerRunning {
		return nil
	}

	fmt.Fprintf(ios.Out, "Healthz:    %s", boolIcon(row.HealthzOK))
	switch {
	case row.HealthzError != "":
		fmt.Fprintf(ios.Out, " (%s)\n", row.HealthzError)
	case row.HealthzStatus != 0 && !row.HealthzOK:
		fmt.Fprintf(ios.Out, " (HTTP %d)\n", row.HealthzStatus)
	default:
		fmt.Fprintln(ios.Out)
	}

	fmt.Fprintf(ios.Out, "Firewall:   %s", boolIcon(row.FirewallReady))
	if row.FirewallError != "" {
		fmt.Fprintf(ios.Out, " (%s)\n", row.FirewallError)
	} else {
		fmt.Fprintln(ios.Out)
	}
	fmt.Fprintf(ios.Out, "Rules:      %d active\n", row.FirewallRuleCnt)
	return nil
}
