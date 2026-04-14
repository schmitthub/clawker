// Package v1 defines the gRPC AdminService for CLI-to-CP communication.
// This is a separate proto package from the agent v1 package, enforcing the
// trust boundary between admin operations (CLI) and agent operations (clawkerd).
package v1

//go:generate moq -rm -pkg mocks -out ../../../internal/controlplane/mocks/admin_client_mock.go . AdminServiceClient

import "github.com/schmitthub/clawker/internal/consts"

// ServiceName is the fully-qualified gRPC service name for AdminService.
const ServiceName = "clawker.admin.v1.AdminService"

// AdminMethodScopes returns the method→scope map for every RPC on
// AdminService. Every method is enforced at the uniform "admin" scope
// (INV-B2-009); future cross-domain methods follow the same policy.
//
// Kept beside the generated bindings so proto additions fail closed: a
// new RPC without a scope entry is caught by
// TestAdminMethodScopes_CoversAllRPCs, which reflects over
// AdminService_ServiceDesc.
func AdminMethodScopes() map[string]string {
	const svc = "/" + ServiceName + "/"
	return map[string]string{
		svc + "FirewallInit":            consts.ScopeAdmin,
		svc + "FirewallRemove":          consts.ScopeAdmin,
		svc + "FirewallEnable":          consts.ScopeAdmin,
		svc + "FirewallDisable":         consts.ScopeAdmin,
		svc + "FirewallBypass":          consts.ScopeAdmin,
		svc + "FirewallAddRules":        consts.ScopeAdmin,
		svc + "FirewallRemoveRules":     consts.ScopeAdmin,
		svc + "FirewallListRules":       consts.ScopeAdmin,
		svc + "FirewallReload":          consts.ScopeAdmin,
		svc + "FirewallStatus":          consts.ScopeAdmin,
		svc + "FirewallRotateCA":        consts.ScopeAdmin,
		svc + "FirewallSyncRoutes":      consts.ScopeAdmin,
		svc + "FirewallResolveHostname": consts.ScopeAdmin,
	}
}
