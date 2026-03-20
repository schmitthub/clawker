package firewall

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// StatusOptions holds the options for the firewall status command.
type StatusOptions struct {
	IOStreams *iostreams.IOStreams
	Firewall  func(context.Context) (firewall.FirewallManager, error)
	Format    *cmdutil.FormatFlags
}

// statusRow is the JSON/template-friendly representation of firewall status.
type statusRow struct {
	Running       bool   `json:"running"`
	EnvoyHealth   bool   `json:"envoy_health"`
	CoreDNSHealth bool   `json:"coredns_health"`
	RuleCount     int    `json:"rule_count"`
	EnvoyIP       string `json:"envoy_ip"`
	CoreDNSIP     string `json:"coredns_ip"`
	NetworkID     string `json:"network_id"`
}

// NewCmdStatus creates the firewall status command.
func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command {
	opts := &StatusOptions{
		IOStreams: f.IOStreams,
		Firewall:  f.Firewall,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show firewall health and status",
		Long: `Display the current health and configuration of the Envoy+CoreDNS
egress firewall, including container health, active rule count, and network info.`,
		Example: `  # Show firewall status
  clawker firewall status

  # Output as JSON
  clawker firewall status --json

  # Custom Go template
  clawker firewall status --format '{{.RuleCount}} rules active'`,
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
	ios := opts.IOStreams

	fwMgr, err := opts.Firewall(ctx)
	if err != nil {
		return fmt.Errorf("connecting to firewall: %w", err)
	}

	status, err := fwMgr.Status(ctx)
	if err != nil {
		return fmt.Errorf("getting firewall status: %w", err)
	}

	row := statusRow{
		Running:       status.Running,
		EnvoyHealth:   status.EnvoyHealth,
		CoreDNSHealth: status.CoreDNSHealth,
		RuleCount:     status.RuleCount,
		EnvoyIP:       status.EnvoyIP,
		CoreDNSIP:     status.CoreDNSIP,
		NetworkID:     status.NetworkID,
	}

	// Format dispatch.
	switch {
	case opts.Format.IsJSON():
		return cmdutil.WriteJSON(ios.Out, row)

	case opts.Format.IsTemplate():
		return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny([]statusRow{row}))

	default:
		cs := ios.ColorScheme()

		healthIcon := func(healthy bool) string {
			if healthy {
				return cs.SuccessIcon()
			}
			return cs.FailureIcon()
		}

		runningText := cs.Red("stopped")
		if status.Running {
			runningText = cs.Green("running")
		}

		fmt.Fprintf(ios.Out, "Firewall:  %s\n", runningText)
		fmt.Fprintf(ios.Out, "Envoy:     %s\n", healthIcon(status.EnvoyHealth))
		fmt.Fprintf(ios.Out, "CoreDNS:   %s\n", healthIcon(status.CoreDNSHealth))
		fmt.Fprintf(ios.Out, "Rules:     %d active\n", status.RuleCount)
		if status.EnvoyIP != "" {
			fmt.Fprintf(ios.Out, "Envoy IP:  %s\n", status.EnvoyIP)
		}
		if status.CoreDNSIP != "" {
			fmt.Fprintf(ios.Out, "DNS IP:    %s\n", status.CoreDNSIP)
		}
		if status.NetworkID != "" {
			fmt.Fprintf(ios.Out, "Network:   %s\n", status.NetworkID)
		}

		return nil
	}
}
