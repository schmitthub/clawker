package controlplane

import "slices"

// ClientRegistration describes a client authorized to obtain JWTs from the
// control plane's /token endpoint. In v1 there is exactly one registered
// client (clawker-cli); follow-up work adds entries here for clawkerd,
// clawker-webui, and so on.
//
// This is a static, in-binary registry. Adding a new client is an explicit
// code change — the CP is a closed system with a small, well-known set of
// machine callers, so dynamic client registration is intentionally not
// supported. The next PR that adds a caller extends `registeredClients` and
// `methodScopes` together.
type ClientRegistration struct {
	// ClientID is the OIDC client_id. It's also the CN of the caller's
	// mTLS client certificate — the authz interceptor cross-checks the
	// mTLS peer CN against the JWT subject claim, so client_id and CN
	// must be the same string for every registered caller.
	ClientID string

	// AuthMethod identifies how the caller authenticates to the /token
	// endpoint. In v1 this is always TLSClientAuth (RFC 8705 mTLS client
	// auth) — the caller's mTLS client cert IS the credential; no
	// client_secret is issued or accepted.
	AuthMethod AuthMethod

	// Scopes enumerates the scopes this client may request when calling
	// /token. Requesting a scope not in this list is rejected.
	// Authorization is per-method: the gRPC interceptor uses the
	// methodScopes map (authz.go) to decide which scope each RPC requires,
	// then checks the JWT's granted scopes against that requirement.
	Scopes []string
}

// AuthMethod identifies how a client authenticates to the OIDC /token
// endpoint. v1 only supports mTLS client auth; the enum exists so future
// PRs can add entries (e.g. ClientSecretBasic for a webui backend) without
// rewriting ClientRegistration.
type AuthMethod int

const (
	// AuthMethodNone is the zero value and rejected everywhere —
	// deliberately not a valid auth method. Prevents zero-value
	// ClientRegistration structs from accidentally authorizing.
	AuthMethodNone AuthMethod = iota

	// TLSClientAuth — RFC 8705. Caller presents an mTLS client cert at
	// the /token endpoint; the cert's CN identifies the client. No
	// client_secret involved.
	TLSClientAuth
)

// Registered scope names. Constants here keep the scope strings in one
// place so the method-scope map (authz.go) and the client registry below
// can't drift out of sync.
const (
	// ScopeFirewallAdmin authorizes all ControlPlaneService firewall/ebpf
	// methods. v1 has one scope; follow-ups can split this into finer
	// grains (e.g. firewall:read, firewall:write, container:enable) as
	// multi-caller authz gets more specific.
	ScopeFirewallAdmin = "firewall:admin"
)

// Known client IDs. Same deduplication reason as the scope constants.
const (
	ClientIDCLI = "clawker-cli"
)

// registeredClients is the static client registry. Keyed by ClientID for
// O(1) lookup in the /token handler and the authz interceptor.
var registeredClients = map[string]ClientRegistration{
	ClientIDCLI: {
		ClientID:   ClientIDCLI,
		AuthMethod: TLSClientAuth,
		Scopes:     []string{ScopeFirewallAdmin},
	},
	// Future entries — each is one struct, zero rewire:
	// "clawkerd":     {ClientID: "clawkerd",     AuthMethod: TLSClientAuth, Scopes: []string{"agent:register", "agent:heartbeat"}},
	// "clawker-webui": {ClientID: "clawker-webui", AuthMethod: ..., Scopes: []string{...}},
}

// LookupClient returns the registration for a client_id, or (zero, false)
// if the caller is unknown. Callers should fail closed on false.
func LookupClient(clientID string) (ClientRegistration, bool) {
	reg, ok := registeredClients[clientID]
	return reg, ok
}

// HasScope returns true if the registration authorizes the given scope.
func (r ClientRegistration) HasScope(scope string) bool {
	return slices.Contains(r.Scopes, scope)
}
