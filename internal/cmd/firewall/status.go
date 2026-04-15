package firewall

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// StatusOptions holds the options for the firewall status command.
type StatusOptions struct {
	IOStreams   *iostreams.IOStreams
	Client      func(context.Context) (*docker.Client, error)
	AdminClient func(context.Context) (adminv1.AdminServiceClient, error)
	Format      *cmdutil.FormatFlags
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
		IOStreams:   f.IOStreams,
		Client:      f.Client,
		AdminClient: f.AdminClient,
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
	// Avoid bootstrapping the CP just to ask about state. If no CP
	// container exists or it's stopped, synthesize a "stopped" row —
	// matches the old host-side daemon `status` contract.
	var row statusRow
	if opts.Client != nil {
		dc, err := opts.Client(ctx)
		if err != nil {
			return fmt.Errorf("connecting to Docker: %w", err)
		}
		running, err := cpboot.CPRunning(ctx, dc)
		if err != nil {
			return fmt.Errorf("checking control plane: %w", err)
		}
		if !running {
			return renderStatus(opts, row)
		}
	}

	adminClient, err := opts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("connecting to control plane: %w", err)
	}

	resp, err := callWithSpinner(ctx, opts.IOStreams, "Fetching firewall status...",
		func(rpcCtx context.Context) (*adminv1.FirewallStatusResult, error) {
			return adminClient.FirewallStatus(rpcCtx, &adminv1.FirewallStatusRequest{})
		})
	if err != nil {
		return wrapRPCError("getting firewall status", err)
	}

	row = statusRow{
		Running:       resp.GetRunning(),
		EnvoyHealth:   resp.GetEnvoyHealth(),
		CoreDNSHealth: resp.GetCorednsHealth(),
		RuleCount:     int(resp.GetRuleCount()),
		EnvoyIP:       resp.GetEnvoyIp(),
		CoreDNSIP:     resp.GetCorednsIp(),
		NetworkID:     resp.GetNetworkId(),
	}

	return renderStatus(opts, row)
}

func renderStatus(opts *StatusOptions, row statusRow) error {
	ios := opts.IOStreams

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
		if row.Running {
			runningText = cs.Green("running")
		}

		fmt.Fprintf(ios.Out, "Firewall:  %s\n", runningText)
		fmt.Fprintf(ios.Out, "Envoy:     %s\n", healthIcon(row.EnvoyHealth))
		fmt.Fprintf(ios.Out, "CoreDNS:   %s\n", healthIcon(row.CoreDNSHealth))
		fmt.Fprintf(ios.Out, "Rules:     %d active\n", row.RuleCount)
		if row.EnvoyIP != "" {
			fmt.Fprintf(ios.Out, "Envoy IP:  %s\n", row.EnvoyIP)
		}
		if row.CoreDNSIP != "" {
			fmt.Fprintf(ios.Out, "DNS IP:    %s\n", row.CoreDNSIP)
		}
		if row.NetworkID != "" {
			fmt.Fprintf(ios.Out, "Network:   %s\n", row.NetworkID)
		}

		return nil
	}
}
