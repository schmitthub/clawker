package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
)

// ---------------------------------------------------------------------------
// INV-B1-016: Admin and agent channels are separate proto packages
// ---------------------------------------------------------------------------

// Tests INV-B1-016 [unit]: Admin and agent proto packages are separate.
// AdminService and AgentService must be in different Go packages with
// different service names. No shared service definition.
func TestINV_B1_016_SeparateProtoPackages(t *testing.T) {
	t.Run("admin service name contains 'admin'", func(t *testing.T) {
		assert.Contains(t, adminv1.ServiceName, "admin",
			"AdminService name must contain 'admin'")
	})

	t.Run("agent service name contains 'agent'", func(t *testing.T) {
		assert.Contains(t, agentv1.ServiceName, "agent",
			"AgentService name must contain 'agent'")
	})

	t.Run("AdminService has correct RPCs", func(t *testing.T) {
		desc := adminv1.AdminService_ServiceDesc
		methods := make(map[string]bool)
		for _, m := range desc.Methods {
			methods[m.MethodName] = true
		}
		for _, s := range desc.Streams {
			methods[s.StreamName] = true
		}

		// AdminService surface: 13 firewall RPCs (INV-B2-009) plus
		// AnnounceAgent and ListAgents.
		expectedRPCs := []string{
			"FirewallInit", "FirewallRemove",
			"FirewallEnable", "FirewallDisable", "FirewallBypass",
			"FirewallAddRules", "FirewallRemoveRule", "FirewallListRules",
			"FirewallReload", "FirewallStatus", "FirewallRotateCA",
			"FirewallSyncRoutes", "FirewallResolveHostname",
			"AnnounceAgent", "ListAgents",
		}
		for _, rpc := range expectedRPCs {
			assert.True(t, methods[rpc],
				"AdminService must have %s RPC", rpc)
		}
	})

	t.Run("AgentService is unary Register only", func(t *testing.T) {
		desc := agentv1.AgentService_ServiceDesc

		// AgentService is the one-shot registration surface clawkerd
		// calls on first boot. Register is unary — flipping it to
		// streaming would silently invert the protocol contract.
		// Pin: zero streaming methods, one unary method named Register.
		assert.Empty(t, desc.Streams, "AgentService must have no streaming RPCs")

		methods := make(map[string]bool)
		for _, m := range desc.Methods {
			methods[m.MethodName] = true
		}
		require.True(t, methods["Register"], "AgentService must have Register RPC")
		assert.Len(t, desc.Methods, 1, "AgentService surface must be Register only — adding RPCs requires a deliberate review of the agent-side trust model")
	})
}
