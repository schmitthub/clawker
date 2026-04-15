package firewall

// Status is the firewall-domain health snapshot returned by Stack.Status.
//
// The field set is intentionally minimal — any new attribute must be
// motivated by a concrete CLI or operator-facing need so this struct
// does not grow into a catch-all.
type Status struct {
	// Running is true only when every managed subsystem is up (Envoy,
	// CoreDNS, control plane). Per-component booleans below disambiguate
	// "partially up" from "fully down".
	Running bool

	// EnvoyHealth / CoreDNSHealth / CPHealth: container exists and is
	// running. Deeper readiness (HTTP /healthz) is probed in WaitForHealthy.
	EnvoyHealth   bool
	CoreDNSHealth bool
	CPHealth      bool

	// RuleCount is the number of normalized/deduplicated egress rules
	// currently loaded in the rules store.
	RuleCount int

	// EnvoyIP / CoreDNSIP / NetworkID: discovered network topology. Empty
	// strings mean the firewall network is not yet created — not an error.
	EnvoyIP   string
	CoreDNSIP string
	NetworkID string
}
