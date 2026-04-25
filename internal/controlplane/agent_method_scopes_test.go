package controlplane

import (
	"testing"

	"github.com/stretchr/testify/assert"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// TestAgentMethodScopes_CoversAllRPCs walks the AgentService proto
// descriptor and asserts every method has a scope entry. Mirror of
// TestAdminMethodScopes_CoversAllRPCs — a future PR that adds an RPC
// without updating AgentMethodScopes fails at build time, not runtime.
func TestAgentMethodScopes_CoversAllRPCs(t *testing.T) {
	scopes := AgentMethodScopes()
	desc := agentv1.AgentService_ServiceDesc
	const svc = "/" + agentv1.ServiceName + "/"

	protoMethods := map[string]bool{}
	for _, m := range desc.Methods {
		protoMethods[svc+m.MethodName] = true
	}
	for _, s := range desc.Streams {
		protoMethods[svc+s.StreamName] = true
	}

	for method := range protoMethods {
		_, ok := scopes[method]
		assert.Truef(t, ok,
			"proto RPC %s has no scope in AgentMethodScopes() — add an entry to enforce auth", method)
	}

	// Catch stale entries: a scope mapping that doesn't correspond to a
	// real RPC.
	for method := range scopes {
		assert.Truef(t, protoMethods[method],
			"AgentMethodScopes() contains %s which is not in AgentService_ServiceDesc — remove stale entry", method)
	}

	assert.Equal(t, len(protoMethods), len(scopes),
		"AgentMethodScopes() count (%d) must equal proto RPC count (%d)", len(scopes), len(protoMethods))
}

// TestAgentMethodScopes_RegisterScope locks the Branch 4 contract: the
// only agent RPC requires the agent:self:register scope. Future RPCs
// can change this expectation, but accidentally widening Register's
// scope (e.g. to `admin`) would silently grant agents privileged
// surface — guard against it.
func TestAgentMethodScopes_RegisterScope(t *testing.T) {
	const want = consts.ScopeAgentSelfRegister
	got := AgentMethodScopes()["/"+agentv1.ServiceName+"/Register"]
	assert.Equal(t, want, got)
}
