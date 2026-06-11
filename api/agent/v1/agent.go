// Package v1 defines the gRPC AgentService for clawkerd-to-CP communication.
//
// AgentService is the agent-side surface clawkerd dials on the CP's
// agent listener on the clawker network. Today the only RPC is Register — the
// one-time CP-driven handshake binding the agent's cert thumbprint to its
// container identity.
package v1

// ServiceName is the fully-qualified gRPC service name for AgentService.
const ServiceName = "clawker.agent.v1.AgentService"

// AgentScope is the OAuth2 scope type for AgentService RPCs. It is a
// distinct named type (NOT an alias of admin's or any other service's
// scope type) so the compiler rejects a cross-service scope: an admin
// scope cannot be placed in AgentMethodScopes, and the agent Hydra client
// cannot be granted a non-agent scope. The wire value is a plain string —
// convert with string() at OAuth2/Hydra boundaries.
type AgentScope string

// ScopeSelfRegister gates clawkerd's Register call — the one-time
// CP-driven handshake where the agent attests its own identity to CP.
const ScopeSelfRegister AgentScope = "agent:self:register"

// AgentMethodScopes maps every AgentService RPC to the scope it requires.
// An AuthInterceptor wired with this map fails closed on unmapped methods
// (returns codes.Unauthenticated), so a new RPC added to the proto
// without a scope entry is rejected at runtime. Mirror of
// AdminMethodScopes; kept beside the generated bindings.
func AgentMethodScopes() map[string]AgentScope {
	return map[string]AgentScope{
		"/" + ServiceName + "/Register": ScopeSelfRegister,
	}
}
