package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
		// ListAgents. AnnounceAgent was retired alongside agentslots.
		expectedRPCs := []string{
			"FirewallInit", "FirewallRemove",
			"FirewallEnable", "FirewallDisable", "FirewallBypass",
			"FirewallAddRules", "FirewallRemoveRule", "FirewallListRules",
			"FirewallReload", "FirewallStatus", "FirewallRotateCA",
			"FirewallSyncRoutes", "FirewallResolveHostname",
			"ListAgents",
		}
		for _, rpc := range expectedRPCs {
			assert.True(t, methods[rpc],
				"AdminService must have %s RPC", rpc)
		}
		assert.Len(t, methods, len(expectedRPCs),
			"AdminService surface drift — methods=%v, expected=%v", methods, expectedRPCs)
	})

	t.Run("AgentService is empty in this branch", func(t *testing.T) {
		desc := agentv1.AgentService_ServiceDesc

		// AgentService is empty after the agentslots/Register retirement.
		// CP→clawkerd command dispatch lives on `ClawkerdService.Session`
		// (CP dials, clawkerd serves) — see api/clawkerd/v1. AgentService
		// stays as the future-extension scaffold for any inbound
		// clawkerd→CP RPC the asymmetric-trust model still permits.
		assert.Empty(t, desc.Methods, "AgentService must have no unary RPCs in this branch (Register retired)")
		assert.Empty(t, desc.Streams, "AgentService must have no streaming RPCs in this branch")
	})
}
