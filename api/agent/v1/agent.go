// Package v1 defines the gRPC AgentService for clawkerd-to-CP communication.
//
// AgentService is the agent-side surface clawkerd dials on the CP's
// agent listener on the clawker network. Register is the one-time
// CP-driven handshake binding the agent's cert thumbprint to its
// container identity; Export is the agent telemetry lane the CP stamps
// and forwards to the collector.
package v1

import "github.com/schmitthub/clawker/internal/consts"

// ServiceName is the fully-qualified gRPC service name for AgentService.
const ServiceName = "clawker.agent.v1.AgentService"

// ExportMaxBodyBytes caps ExportRequest.body. 4 MiB matches the OTLP
// SDK default batch ceiling; the clawkerd relay rejects a larger POST
// before it reaches the wire and the CP handler rejects it again with
// codes.ResourceExhausted.
const ExportMaxBodyBytes = 4 << 20

// ExportMaxMessageBytes is the agent listener's grpc.MaxRecvMsgSize: the
// body cap plus room for the request envelope, so a body at the cap is
// not rejected by the transport before the handler sees it.
const ExportMaxMessageBytes = ExportMaxBodyBytes + 64<<10

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
	const svc = "/" + ServiceName + "/"
	return map[string]AgentScope{
		svc + "Register": ScopeSelfRegister,
		// Export is PUBLIC: clawkerd spends its single-use Hydra
		// assertion on Register and cannot mint a second token. mTLS at
		// the listener plus the IdentityInterceptor and the handler's
		// registry thumbprint check are the gate.
		svc + "Export": consts.ScopePublic,
	}
}
