package controlplane

import (
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// AgentMethodScopes maps every clawker.agent.v1.AgentService RPC to the
// OAuth2 scope it requires. Agents call these via the CP's clawker-net
// listener; AuthInterceptor wired with this map fails closed on
// unmapped methods (returns codes.Unauthenticated), so a new RPC added
// to the proto without a scope entry is rejected at runtime — and
// `TestAgentMethodScopes_CoversAllRPCs` rejects it at build time.
//
// Branch 4 ships only Register; the agent assertion Hydra issues a
// token with `agent:self:register` scope, and that's what this method
// requires. Future agent RPCs (event reporting, command receivers) land
// alongside their own scopes.
func AgentMethodScopes() map[string]string {
	const svc = "/" + agentv1.ServiceName + "/"
	return map[string]string{
		svc + "Register": consts.ScopeAgentSelfRegister,
	}
}
