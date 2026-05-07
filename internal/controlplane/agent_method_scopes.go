package controlplane

import (
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// AgentMethodScopes maps every clawker.agent.v1.AgentService RPC to the
// OAuth2 scope it requires. Agents call these via the CP's clawker-net
// listener; AuthInterceptor wired with this map fails closed on
// unmapped methods (returns codes.Unauthenticated), so a new RPC added
// to the proto without a scope entry is rejected at runtime.
//
// Register is the one-time-per-container CP-driven handshake — the
// scope name reflects "agent attests its own identity to CP" semantics.
func AgentMethodScopes() map[string]string {
	return map[string]string{
		"/" + agentv1.ServiceName + "/Register": consts.ScopeAgentSelfRegister,
	}
}
