package auth

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// AgentName is the user-typed short agent name (e.g. "dev"). It is
// distinct from a `string` so callers cannot accidentally pass:
//   - the canonical form ("clawker.foo.bar") — the helpers compose
//     that themselves;
//   - a name containing "." — segment counting breaks downstream
//     wherever the canonical "clawker.<project>.<agent>" form is parsed
//     or filtered;
//   - arbitrary characters that wouldn't survive Docker's container/
//     volume naming or the canonical-CN compose rules.
//
// Construction goes through NewAgentName, which enforces the contract.
// In-package code that already trusts a value can convert via the
// String() accessor; out-of-package callers can't read the underlying
// string except through that accessor (no exported field).
type AgentName struct{ s string }

// NewAgentName parses + validates a user-typed agent short name and
// returns a typed value. Returns an error on empty input, names that
// look like the canonical form ("clawker.<...>"), names containing
// disallowed characters, or names exceeding the length cap. The error
// message names the offending input so a CLI surfaces the violation
// without the user guessing what was wrong.
func NewAgentName(s string) (AgentName, error) {
	if s == "" {
		return AgentName{}, fmt.Errorf("agent name required")
	}
	if err := validateShortName("agent name", s); err != nil {
		return AgentName{}, err
	}
	return AgentName{s: s}, nil
}

// String returns the underlying short name.
func (a AgentName) String() string { return a.s }

// MustAgentName wraps a string that the caller has ALREADY validated
// (e.g. read back from a registry entry that was inserted via a typed
// path, or composed in tests). Panics if the input fails the
// AgentName contract — invariant violation that must surface loudly,
// not silently malformed identity downstream.
//
// Production code should prefer NewAgentName + error handling at
// every wire/CLI boundary. MustAgentName exists for places where the
// validation already ran upstream and the boundary code holds a raw
// string (e.g. existing struct fields whose typing isn't yet migrated).
func MustAgentName(s string) AgentName {
	a, err := NewAgentName(s)
	if err != nil {
		panic("auth: MustAgentName invariant violated: " + err.Error())
	}
	return a
}

// IsZero reports whether this is the zero value (uninitialized
// AgentName{}). The constructors always reject empty input, so a real
// AgentName is never zero — IsZero is for callers who hold a value of
// unknown provenance.
func (a AgentName) IsZero() bool { return a.s == "" }

// ProjectSlug is the user-typed project slug (e.g. "myapp"). Like
// AgentName but allows the empty value (matches docker.ContainerName's
// 2-segment naming case where no project is configured).
type ProjectSlug struct{ s string }

// NewProjectSlug parses + validates a user-typed project slug. Empty
// input is allowed and returns the zero value (interpreted downstream
// as "unscoped project, 2-segment naming"). Non-empty inputs must
// satisfy the same charset/length contract as AgentName.
func NewProjectSlug(s string) (ProjectSlug, error) {
	if s == "" {
		return ProjectSlug{}, nil
	}
	if err := validateShortName("project slug", s); err != nil {
		return ProjectSlug{}, err
	}
	return ProjectSlug{s: s}, nil
}

// String returns the underlying slug; empty for the unscoped case.
func (p ProjectSlug) String() string { return p.s }

// MustProjectSlug is the unchecked-but-assertive companion to
// NewProjectSlug. See MustAgentName for the rationale and usage rule.
func MustProjectSlug(s string) ProjectSlug {
	p, err := NewProjectSlug(s)
	if err != nil {
		panic("auth: MustProjectSlug invariant violated: " + err.Error())
	}
	return p
}

// IsEmpty reports whether the slug is the empty/unscoped value. Use
// this rather than `s.String() == ""` so a future refactor that changes
// the underlying representation doesn't silently break the empty
// check.
func (p ProjectSlug) IsEmpty() bool { return p.s == "" }

// shortNameRE matches the strict subset of characters allowed in
// AgentName / ProjectSlug. Stricter than docker.ValidateResourceName:
// no dot characters allowed, because the canonical CN format
// "clawker.<project>.<agent>" relies on dots being segment separators
// — a dot inside a name corrupts every downstream parser. First
// character must be alphanumeric so the resulting canonical CN doesn't
// start with a non-printable run.
var shortNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// shortNameMax caps the length so the composed Docker container name
// ("clawker.<project>.<agent>") and volume name
// ("clawker.<project>.<agent>-<purpose>") stay under Docker's 128-byte
// resource-name limit. 50 leaves comfortable headroom for the prefix +
// purpose suffixes (longest is "workspace") while accepting any
// docker.GenerateRandomName output (worst-case adj-noun ≈ 27 chars).
//
// The canonical agent CN ("clawker.<project>.<agent>") is no longer
// constrained by x509's 64-byte CommonName limit because MintAgentCert
// embeds it as a URI SAN (urn:clawker:agent:<canonical>) rather than as
// the cert's Subject.CommonName. The cert CN is the deterministic
// consts.ContainerClawkerd literal.
const shortNameMax = 50

// canonicalPrefix is the literal prefix that distinguishes a canonical
// agent name from a user-typed short name. Reject inputs that already
// look canonical so a wrapping layer never produces "clawker.clawker.
// <project>.<agent>" by accident.
const canonicalPrefix = consts.NamePrefix + "."

func validateShortName(role, s string) error {
	if len(s) > shortNameMax {
		return fmt.Errorf("%s %q: too long (max %d chars)", role, s, shortNameMax)
	}
	if strings.HasPrefix(s, canonicalPrefix) {
		return fmt.Errorf("%s %q: must be the user-typed short name, not the canonical %q form", role, s, canonicalPrefix+"...")
	}
	if !shortNameRE.MatchString(s) {
		return fmt.Errorf("%s %q: must match [a-zA-Z0-9][a-zA-Z0-9_-]* (no dots, no spaces, alphanumeric start)", role, s)
	}
	return nil
}
