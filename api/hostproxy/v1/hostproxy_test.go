package v1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
)

// TestHostProxyMethodScopes_CoversAllRPCs pins the scope map to the proto
// service descriptor: an RPC added to hostproxy.proto without a scope
// entry is rejected fail-closed at runtime, and a stale entry for a
// removed RPC is a wiring smell. Mirrors TestAgentMethodScopes_CoversAllRPCs.
func TestHostProxyMethodScopes_CoversAllRPCs(t *testing.T) {
	scopes := hostproxyv1.HostProxyMethodScopes()
	desc := hostproxyv1.HostProxyService_ServiceDesc

	protoMethods := make(map[string]struct{})
	for _, m := range desc.Methods {
		protoMethods["/"+desc.ServiceName+"/"+m.MethodName] = struct{}{}
	}
	for _, s := range desc.Streams {
		protoMethods["/"+desc.ServiceName+"/"+s.StreamName] = struct{}{}
	}

	for method := range protoMethods {
		_, ok := scopes[method]
		assert.True(t, ok, "proto RPC %s has no scope in HostProxyMethodScopes()", method)
	}
	for method := range scopes {
		_, ok := protoMethods[method]
		assert.True(t, ok, "HostProxyMethodScopes() contains %s which is not in HostProxyService_ServiceDesc", method)
	}
}

// TestHostProxyMethodScopes_ConnectNeedsServeScope pins Connect to the
// host proxy's own scope: it is never public and never the admin scope.
func TestHostProxyMethodScopes_ConnectNeedsServeScope(t *testing.T) {
	scopes := hostproxyv1.HostProxyMethodScopes()
	assert.Equal(t, hostproxyv1.ScopeServe, scopes[hostproxyv1.HostProxyService_Connect_FullMethodName])
	assert.Equal(t, "/"+hostproxyv1.ServiceName+"/", hostproxyv1.ServicePrefix)
}
