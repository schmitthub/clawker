package auth

import "fmt"

// AgentName is the user-typed short agent name (e.g. "dev"). It is
// distinct from a `string` purely for compile-time discipline — callers
// can't accidentally pass an arbitrary string where AgentName is
// expected. The constructor performs no runtime validation; bad input
// errors at the actual downstream consumer (Docker container/volume
// create, x509 URI SAN encoding, gRPC IdentityInterceptor's symmetric
// SAN-vs-label compare). Pre-validation at this layer would duplicate
// what those layers already enforce.
//
// Input normalization for project/agent names derived from filesystem
// paths or user flags happens upstream in `cmdutil.ProjectSlugify`
// before the value crosses into this package.
type AgentName struct{ s string }

// NewAgentName wraps a string as an AgentName. Returns an error only
// when the input is empty — every other constraint is enforced at the
// consumer (Docker, x509, CP IdentityInterceptor). The error return
// stays on the signature so existing callers don't need to change
// shape; the only error today is "agent name required".
func NewAgentName(s string) (AgentName, error) {
	if s == "" {
		return AgentName{}, fmt.Errorf("agent name required")
	}
	return AgentName{s: s}, nil
}

// String returns the underlying short name.
func (a AgentName) String() string { return a.s }

// MustAgentName wraps a string that the caller has ALREADY checked for
// emptiness (e.g. composed in tests, or read back from a registry
// entry inserted through NewAgentName). Panics on empty input —
// invariant violation that must surface loudly rather than land a
// silently-zero identity downstream.
func MustAgentName(s string) AgentName {
	a, err := NewAgentName(s)
	if err != nil {
		panic("auth: MustAgentName invariant violated: " + err.Error())
	}
	return a
}

// IsZero reports whether this is the zero value (uninitialized
// AgentName{}). The constructors reject empty input, so a real
// AgentName is never zero — IsZero is for callers holding a value of
// unknown provenance.
func (a AgentName) IsZero() bool { return a.s == "" }

// Less reports whether a sorts before other in lexicographic order on
// the underlying short name. Lives on AgentName so the sort sites
// under internal/controlplane/agent/registry*.go don't reach for
// `.String() < .String()` (which would silently drop the type-safety
// the rest of this file gives). Treats the zero value as the smallest
// element so snapshot output stays deterministic.
func (a AgentName) Less(other AgentName) bool { return a.s < other.s }

// ProjectSlug is the user-typed project slug (e.g. "myapp"). Like
// AgentName, the type exists for compile-time discipline; no runtime
// validation runs at construction. Unlike AgentName, the empty value
// is legitimate — it signals a global-scope agent (no project
// namespace), producing the 2-segment naming case documented at
// internal/consts/consts.go (running `clawker` outside a registered
// project).
type ProjectSlug struct{ s string }

// NewProjectSlug wraps a string as a ProjectSlug. Always returns nil
// error — the signature retains `error` only so existing callers
// don't need to change shape if the validation ever has to return.
// Empty input produces the zero value (the global-scope-agent
// signal — no project namespace).
func NewProjectSlug(s string) (ProjectSlug, error) {
	return ProjectSlug{s: s}, nil
}

// String returns the underlying slug; empty for the global-scope case.
func (p ProjectSlug) String() string { return p.s }

// MustProjectSlug is the unchecked companion to NewProjectSlug. Kept
// for callers (and tests) that prefer a single-return form. Since
// NewProjectSlug can't error, MustProjectSlug can't panic — but the
// name pairs with MustAgentName for the same construction-site
// readability.
func MustProjectSlug(s string) ProjectSlug {
	p, _ := NewProjectSlug(s)
	return p
}

// IsEmpty reports whether the slug is the empty global-scope value.
// Use this rather than `s.String() == ""` so a future refactor of the
// underlying representation doesn't silently break the check.
func (p ProjectSlug) IsEmpty() bool { return p.s == "" }

// Less reports whether p sorts before other in lexicographic order on
// the underlying slug. The empty global-scope slug sorts before every
// non-empty value, which keeps Snapshot output deterministic for the
// docker.ContainerName 2-segment case.
func (p ProjectSlug) Less(other ProjectSlug) bool { return p.s < other.s }
