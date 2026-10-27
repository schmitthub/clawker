// Package v1 defines the gRPC HostProxyService: the host proxy's outbound
// stream to the CP admin listener, on which the CP sends authorized host
// actions and the host proxy returns the results.
package v1

// ServiceName is the fully-qualified gRPC service name for HostProxyService.
const ServiceName = "clawker.hostproxy.v1.HostProxyService"

// ServicePrefix is the gRPC full-method prefix of every HostProxyService
// RPC. The admin listener routes calls with this prefix to the host proxy
// auth interceptor.
const ServicePrefix = "/" + ServiceName + "/"

// HostProxyScope is the OAuth2 scope type for HostProxyService RPCs. It is
// a distinct named type so the compiler rejects a scope of another service
// in HostProxyMethodScopes. Convert with string() at OAuth2/Hydra
// boundaries.
type HostProxyScope string

// ScopeServe gates Connect. Only the host proxy's Hydra client holds it.
const ScopeServe HostProxyScope = "hostproxy:serve"

// HostProxyMethodScopes maps every HostProxyService RPC to the scope it
// requires. An AuthInterceptor wired with this map fails closed on
// unmapped methods.
func HostProxyMethodScopes() map[string]HostProxyScope {
	return map[string]HostProxyScope{
		ServicePrefix + "Connect": ScopeServe,
	}
}
