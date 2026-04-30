package controlplane

// AgentMethodScopes maps every clawker.agent.v1.AgentService RPC to the
// OAuth2 scope it requires. Agents call these via the CP's clawker-net
// listener; AuthInterceptor wired with this map fails closed on
// unmapped methods (returns codes.Unauthenticated), so a new RPC added
// to the proto without a scope entry is rejected at runtime — and
// `TestAgentMethodScopes_CoversAllRPCs` rejects it at build time.
//
// AgentService proto is empty in this branch. Future inbound
// clawkerd→CP RPCs add their scope entry here.
func AgentMethodScopes() map[string]string {
	return map[string]string{}
}
