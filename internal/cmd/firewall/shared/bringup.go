package shared

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// BringUpStack sends the idempotent FirewallInit RPC under a spinner
// with the shared bringup deadline, prints the stack-up summary on
// success, and on failure prints the stack-down exposure warning and
// returns the remediation-wrapped error. Shared by `firewall up` and
// `controlplane up` (which brings the stack up when firewall.enable is
// set in settings.yaml) so both verbs present identical bringup UX.
// The caller owns CP bootstrap and the AdminClient dial.
func BringUpStack(ctx context.Context, ios *iostreams.IOStreams, client adminv1.AdminServiceClient) error {
	resp, err := CallWithSpinnerTimeout(ctx, ios, "Starting firewall stack...",
		consts.FirewallStackBringupRPCTimeout,
		func(rpcCtx context.Context) (*adminv1.FirewallInitResult, error) {
			return client.FirewallInit(rpcCtx, &adminv1.FirewallInitRequest{})
		})
	if err != nil {
		WarnStackDownExposure(ios)
		return WrapRPCError("starting firewall", err)
	}

	fmt.Fprintf(ios.Out, "%s Firewall stack up\n", ios.ColorScheme().SuccessIcon())
	if resp.GetEnvoyIp() != "" {
		fmt.Fprintf(ios.Out, "  Envoy:    %s\n", resp.GetEnvoyIp())
	}
	if resp.GetCorednsIp() != "" {
		fmt.Fprintf(ios.Out, "  CoreDNS:  %s\n", resp.GetCorednsIp())
	}
	if resp.GetNetworkId() != "" {
		fmt.Fprintf(ios.Out, "  Network:  %s\n", resp.GetNetworkId())
	}

	return nil
}
