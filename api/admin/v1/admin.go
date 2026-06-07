// Package v1 defines the gRPC AdminService for CLI-to-CP communication.
// This is a separate proto package from the agent v1 package, enforcing the
// trust boundary between admin operations (CLI) and agent operations (clawkerd).
package v1

//go:generate moq -rm -pkg mocks -out ../../../internal/controlplane/mocks/admin_client_mock.go . AdminServiceClient

import "github.com/schmitthub/clawker/internal/consts"

// ServiceName is the fully-qualified gRPC service name for AdminService.
const ServiceName = "clawker.admin.v1.AdminService"

// PublicScope is the scope value marking an RPC as intentionally public — no
// bearer token required (mTLS at the listener still authenticates the channel).
// It is a named sentinel so an intentional public method in AdminMethodScopes
// reads as a deliberate choice rather than an accidental empty string. The
// AuthInterceptor treats any empty required scope as public (it serves both
// admin and agent scope maps, so it matches the bare value rather than this
// package-specific symbol).
const PublicScope = ""

// AdminMethodScopes returns the method→scope map for every RPC on
// AdminService. Every method is enforced at the uniform "admin" scope
// (INV-B2-009) EXCEPT GetSystemTime, which is intentionally public (empty
// scope = no bearer token; see the AuthInterceptor public-method branch).
// Future cross-domain methods follow the uniform-admin policy unless they
// have an equally fundamental bootstrap reason to be public.
//
// Kept beside the generated bindings so proto additions fail closed: a
// new RPC without a scope entry is caught by
// TestAdminMethodScopes_CoversAllRPCs, which reflects over
// AdminService_ServiceDesc.
func AdminMethodScopes() map[string]string {
	const svc = "/" + ServiceName + "/"
	return map[string]string{
		// GetSystemTime is PUBLIC: the CLI calls it before it can mint an
		// access token, to align its OAuth2 client-assertion `iat` to the CP's
		// clock. Gating it on a token would be circular. mTLS at the listener
		// still authenticates the channel.
		svc + "GetSystemTime": PublicScope,

		svc + "FirewallInit":            consts.ScopeAdmin,
		svc + "FirewallRemove":          consts.ScopeAdmin,
		svc + "FirewallEnable":          consts.ScopeAdmin,
		svc + "FirewallDisable":         consts.ScopeAdmin,
		svc + "FirewallBypass":          consts.ScopeAdmin,
		svc + "FirewallAddRules":        consts.ScopeAdmin,
		svc + "FirewallRemoveRule":      consts.ScopeAdmin,
		svc + "FirewallListRules":       consts.ScopeAdmin,
		svc + "FirewallReload":          consts.ScopeAdmin,
		svc + "FirewallStatus":          consts.ScopeAdmin,
		svc + "FirewallRotateCA":        consts.ScopeAdmin,
		svc + "FirewallSyncRoutes":      consts.ScopeAdmin,
		svc + "FirewallResolveHostname": consts.ScopeAdmin,
		svc + "ListAgents":              consts.ScopeAdmin,
	}
}
