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
// Register (one-shot registration handshake) requires the
// `agent:self:register` scope — the only scope Hydra grants to the
// agent OAuth2 client. Future per-agent RPCs land alongside with their
// own scopes.
func AgentMethodScopes() map[string]string {
	const svc = "/" + agentv1.ServiceName + "/"
	return map[string]string{
		svc + "Register": consts.ScopeAgentSelfRegister,
	}
}
