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
// Both Connect (lifetime command channel) and Events (telemetry stream,
// stub in this branch) require the `agent:self:register` scope — the
// only scope Hydra grants to the agent OAuth2 client. B5 may split
// scopes when Events grows a real payload.
func AgentMethodScopes() map[string]string {
	const svc = "/" + agentv1.ServiceName + "/"
	return map[string]string{
		svc + "Connect": consts.ScopeAgentSelfRegister,
		svc + "Events":  consts.ScopeAgentSelfRegister,
	}
}
