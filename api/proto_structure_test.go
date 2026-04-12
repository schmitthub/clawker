package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
	t.Run("admin and agent service names are distinct", func(t *testing.T) {
		assert.NotEqual(t, adminv1.ServiceName, agentv1.ServiceName,
			"AdminService and AgentService must have different service names")
	})

	t.Run("admin service name contains 'admin'", func(t *testing.T) {
		assert.Contains(t, adminv1.ServiceName, "admin",
			"AdminService name must contain 'admin'")
	})

	t.Run("agent service name contains 'agent'", func(t *testing.T) {
		assert.Contains(t, agentv1.ServiceName, "agent",
			"AgentService name must contain 'agent'")
	})

	t.Run("admin service is in admin/v1 package", func(t *testing.T) {
		// If the import compiled, the package exists at the right path.
		// This is a compile-time assertion via the import.
		assert.NotEmpty(t, adminv1.ServiceName,
			"AdminService must be defined in api/admin/v1/")
	})

	t.Run("agent service is in agent/v1 package", func(t *testing.T) {
		assert.NotEmpty(t, agentv1.ServiceName,
			"AgentService must be defined in api/agent/v1/")
	})

	t.Run("no shared service between packages", func(t *testing.T) {
		assert.NotEqual(t, adminv1.ServiceName, agentv1.ServiceName,
			"admin and agent services must not share a service definition")
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

		expectedRPCs := []string{
			"Install", "Remove", "Enable", "Disable",
			"SyncRoutes", "ResolveHostname",
		}
		for _, rpc := range expectedRPCs {
			assert.True(t, methods[rpc],
				"AdminService must have %s RPC", rpc)
		}
	})

	t.Run("AdminService registered on gRPC server", func(t *testing.T) {
		// Verify the generated registration function exists.
		srv := grpc.NewServer() //nolint:staticcheck // nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection -- test-only, no TLS needed
		// This compiles only if the generated interface + registration exist.
		adminv1.RegisterAdminServiceServer(srv, nil)
	})
}
