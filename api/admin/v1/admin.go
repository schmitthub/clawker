// Package v1 defines the gRPC AdminService for CLI-to-CP communication.
// This is a separate proto package from the agent v1 package, enforcing the
// trust boundary between admin operations (CLI) and agent operations (clawkerd).
package v1

//go:generate moq -rm -pkg mocks -out ../../../internal/controlplane/mocks/admin_client_mock.go . AdminServiceClient

import "github.com/schmitthub/clawker/internal/consts"

// ServiceName is the fully-qualified gRPC service name for AdminService.
const ServiceName = "clawker.admin.v1.AdminService"

// AdminScope is the OAuth2 scope type for AdminService RPCs. It is a
// distinct named type (NOT an alias of agent's or any other service's
// scope type) so the compiler rejects a cross-service scope: an
// agent scope cannot be placed in AdminMethodScopes, and the admin Hydra
// client cannot be granted a non-admin scope. The wire value is a plain
// string — convert with string() at OAuth2/Hydra boundaries.
type AdminScope string

// ScopeAdmin is the uniform scope every AdminService RPC requires
// (INV-B2-009), with the lone exception of the public GetSystemTime.
const ScopeAdmin AdminScope = "admin"

// AdminMethodScopes returns the method→scope map for every RPC on
// AdminService. Every method is enforced at the uniform admin scope
// (INV-B2-009) EXCEPT GetSystemTime, which is mapped to the public scope
// (consts.ScopePublic) — no bearer token, served on mTLS alone; see the
// AuthInterceptor public branch. Future cross-domain methods follow the
// uniform-admin policy unless they have an equally fundamental bootstrap
// reason to be public.
//
// Kept beside the generated bindings so proto additions fail closed: a
// new RPC without a scope entry is caught by
// TestAdminMethodScopes_CoversAllRPCs, which reflects over
// AdminService_ServiceDesc.
func AdminMethodScopes() map[string]AdminScope {
	const svc = "/" + ServiceName + "/"
	return map[string]AdminScope{
		// GetSystemTime is PUBLIC: the CLI calls it before it can mint an
		// access token, to align its OAuth2 client-assertion `iat` to the
		// CP's clock. Gating it on a token would be circular. mTLS at the
		// listener still authenticates the channel.
		svc + "GetSystemTime": consts.ScopePublic,

		// Privileged RPCs on this service are admin-only.
		svc + "FirewallInit":            ScopeAdmin,
		svc + "FirewallRemove":          ScopeAdmin,
		svc + "FirewallEnable":          ScopeAdmin,
		svc + "FirewallDisable":         ScopeAdmin,
		svc + "FirewallBypass":          ScopeAdmin,
		svc + "FirewallAddRules":        ScopeAdmin,
		svc + "FirewallRemoveRule":      ScopeAdmin,
		svc + "FirewallListRules":       ScopeAdmin,
		svc + "FirewallReload":          ScopeAdmin,
		svc + "FirewallStatus":          ScopeAdmin,
		svc + "FirewallRotateCA":        ScopeAdmin,
		svc + "FirewallSyncRoutes":      ScopeAdmin,
		svc + "FirewallResolveHostname": ScopeAdmin,
		svc + "ListAgents":              ScopeAdmin,
	}
}
