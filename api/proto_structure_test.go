package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

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

	t.Run("AgentService has Connect and Events streaming RPCs", func(t *testing.T) {
		desc := agentv1.AgentService_ServiceDesc
		streamsByName := make(map[string]grpc.StreamDesc)
		for _, s := range desc.Streams {
			streamsByName[s.StreamName] = s
		}

		// Streaming directions are load-bearing: Connect is the CP→clawkerd
		// command channel (server-streaming); Events is the clawkerd→CP
		// telemetry channel (client-streaming). Flipping either would
		// silently invert the protocol contract — pin both flags here.
		connect, ok := streamsByName["Connect"]
		require.True(t, ok, "AgentService must have Connect RPC")
		assert.True(t, connect.ServerStreams, "Connect must be server-streaming")
		assert.False(t, connect.ClientStreams, "Connect must NOT be client-streaming")

		events, ok := streamsByName["Events"]
		require.True(t, ok, "AgentService must have Events RPC")
		assert.True(t, events.ClientStreams, "Events must be client-streaming")
		assert.False(t, events.ServerStreams, "Events must NOT be server-streaming")
	})
}
