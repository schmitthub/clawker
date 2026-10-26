package v1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// TestAgentMethodScopes_CoversAllRPCs pins the scope map to the proto
// service descriptor: an RPC added to agent.proto without a scope entry
// is rejected fail-closed at runtime, and a stale entry for a removed RPC
// is a wiring smell. Mirrors TestAdminMethodScopes_CoversAllRPCs.
func TestAgentMethodScopes_CoversAllRPCs(t *testing.T) {
	scopes := agentv1.AgentMethodScopes()
	desc := agentv1.AgentService_ServiceDesc

	protoMethods := make(map[string]struct{})
	for _, m := range desc.Methods {
		protoMethods["/"+desc.ServiceName+"/"+m.MethodName] = struct{}{}
	}
	for _, s := range desc.Streams {
		protoMethods["/"+desc.ServiceName+"/"+s.StreamName] = struct{}{}
	}

	for method := range protoMethods {
		_, ok := scopes[method]
		assert.True(t, ok, "proto RPC %s has no scope in AgentMethodScopes()", method)
	}
	for method := range scopes {
		_, ok := protoMethods[method]
		assert.True(t, ok, "AgentMethodScopes() contains %s which is not in AgentService_ServiceDesc", method)
	}
}

// TestAgentMethodScopes_ExportTelemetryIsPublic pins the ExportTelemetry lane's auth shape:
// no bearer token (clawkerd's single-use Hydra assertion is spent on
// Register), mTLS plus the identity interceptor only.
func TestAgentMethodScopes_ExportTelemetryIsPublic(t *testing.T) {
	scopes := agentv1.AgentMethodScopes()
	scope, ok := scopes["/"+agentv1.ServiceName+"/ExportTelemetry"]
	require.True(t, ok)
	assert.Equal(t, agentv1.AgentScope(consts.ScopePublic), scope)
	assert.Equal(t, agentv1.ScopeSelfRegister, scopes["/"+agentv1.ServiceName+"/Register"],
		"Register stays bearer-gated")
}
