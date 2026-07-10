package consts

import (
	"fmt"
	"regexp"
	"strings"
)

// NameMaxLength is the maximum length for a name governed by the unified
// naming rule below.
const NameMaxLength = 32

// nameRe is the unified naming rule shared by every clawker-registered
// dev-stack surface: stack names, harness names, and the registry/overlay
// keys that key them (a stacks:/harnesses: registry entry, or a
// build.harnesses.<name> overlay key). One rule everywhere a name becomes a
// directory name, a registry key, and — for harnesses — a Docker image tag
// segment: lowercase letters, digits, and internal hyphens (consecutive
// hyphens allowed), no leading or trailing hyphen.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateName rejects a name that does not match the unified naming rule
// (lowercase kebab-case, 1-NameMaxLength characters). This is the single
// naming rule for stack names, harness names, stack/harness registry keys,
// and build.harnesses overlay keys — never a per-surface regex.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > NameMaxLength {
		return fmt.Errorf("name %q must be between 1 and %d characters", name, NameMaxLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf(
			"name %q is invalid: must match %s (lowercase letters, digits, and internal hyphens; no leading or trailing hyphen)",
			name,
			nameRe.String(),
		)
	}
	return nil
}

// ValidateHarnessName applies ValidateName plus the harness-specific
// reservation: a harness registry key doubles as its built image's tag, so
// it may not collide with a reserved tag alias.
func ValidateHarnessName(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	switch name {
	case ImageTagDefaultAlias, ImageTagLatest, ImageTagBase:
		return fmt.Errorf("name %q is reserved as an image tag alias", name)
	}
	return nil
}

// addressSeparator joins the three segments of a qualified component address
// (namespace.bundle.component). It is also the segment separator every surface
// that spells a qualified name uses — yaml keys, CLI selectors, image tags,
// volume names, index names — so there is exactly one spelling everywhere.
const addressSeparator = "."

// addressSegments is the exact segment count of a qualified address.
const addressSegments = 3

// ReservedNamespaceOfficial is reserved alongside the clawker-branded
// namespaces (see ValidateNamespace) so a distributed bundle cannot masquerade
// as a first-party or officially-endorsed source.
const ReservedNamespaceOfficial = "official"

// address is the parsed form of a component address: a bare name (qualified
// false, only name set) or a fully-qualified namespace.bundle.component triple.
type address struct {
	namespace string
	bundle    string
	name      string
	qualified bool
}

// parseAddress is the structural parser behind SplitAddress and the Validate*
// helpers. It classifies s as bare or qualified and validates every segment
// against the shared name rule. It is structural only: it does NOT apply the
// reserved namespace rule (that is ValidateNamespace / ValidateAddress).
func parseAddress(s string) (address, error) {
	if !strings.Contains(s, addressSeparator) {
		if err := ValidateName(s); err != nil {
			return address{}, err
		}
		return address{namespace: "", bundle: "", name: s, qualified: false}, nil
	}
	segs := strings.Split(s, addressSeparator)
	if len(segs) != addressSegments {
		return address{}, fmt.Errorf(
			"address %q is invalid: must be a bare name or a qualified namespace%sbundle%scomponent address",
			s, addressSeparator, addressSeparator,
		)
	}
	for _, seg := range segs {
		if err := ValidateName(seg); err != nil {
			return address{}, fmt.Errorf("address %q is invalid: %w", s, err)
		}
	}
	return address{namespace: segs[0], bundle: segs[1], name: segs[2], qualified: true}, nil
}

// SplitAddress classifies s as either a bare name (embedded floor / loose
// local tier) or a fully-qualified namespace.bundle.component address
// (installed bundle tier), validating every segment against the shared name
// rule. A bare name (no separator) returns qualified=false with only name set;
// a qualified name must have exactly three separator-delimited segments, each
// ValidateName-conformant, returned as namespace/bundle/name with
// qualified=true. Any other segment count, or a non-conformant segment, is an
// error. SplitAddress is structural only: it does NOT apply the reserved
// namespace rule (that is ValidateNamespace / ValidateAddress).
func SplitAddress(s string) (string, string, string, bool, error) {
	a, err := parseAddress(s)
	return a.namespace, a.bundle, a.name, a.qualified, err
}

// JoinAddress spells a fully-qualified component address from its three
// segments, joined by the address separator — namespace.bundle.component. It is
// the inverse of SplitAddress and the single formatting helper every surface
// that emits a qualified name uses (yaml keys, CLI selectors, image tags,
// volume names, index names), so the dotted spelling is never hardcoded. It
// does not validate; callers that accept untrusted segments validate them with
// ValidateName / ValidateAddress first.
func JoinAddress(namespace, bundle, name string) string {
	return namespace + addressSeparator + bundle + addressSeparator + name
}

// JoinIdentity spells a bundle identity — the (namespace, bundle-name) pair,
// the first two segments of the address grammar — as namespace.name. It is the
// spelling bundle-level surfaces use (clawker bundle remove/update arguments,
// collision errors, listings); like JoinAddress it exists so the dotted
// spelling is never hardcoded. It does not validate.
func JoinIdentity(namespace, name string) string {
	return namespace + addressSeparator + name
}

// ValidateAddress validates s as either a bare name or a fully-qualified
// namespace.bundle.component address, additionally rejecting a qualified
// address whose namespace segment is reserved (see ValidateNamespace). This is
// the reserved-aware form validator; ValidateComponentRef is the lenient
// selection-key variant that skips the reserved gate.
func ValidateAddress(s string) error {
	a, err := parseAddress(s)
	if err != nil {
		return err
	}
	if a.qualified {
		return ValidateNamespace(a.namespace)
	}
	return nil
}

// ValidateComponentRef validates a component selection key — a build.stacks
// entry, a monitor.extensions entry, or a harness -t segment. A bare key must
// satisfy the shared name rule; a qualified key must be a three-segment
// namespace.bundle.component address with every segment conformant. It is
// intentionally structural: the reserved-namespace gate lives at the bundle
// manifest front door (ValidateNamespace), and the image-tag alias reservation
// applies only to bare harness names via ValidateHarnessName — neither is
// re-checked on every selection key.
func ValidateComponentRef(ref string) error {
	_, err := parseAddress(ref)
	return err
}

// ValidateHarnessRef validates a harness selection key — a -t tag segment, a
// harnesses: init-config key, or a build.harnesses: overlay key. A qualified
// key is checked structurally like any component ref; a bare key must
// additionally avoid the reserved image-tag aliases (default/latest/base),
// which can never collide with a dotted qualified spelling.
func ValidateHarnessRef(ref string) error {
	a, err := parseAddress(ref)
	if err != nil {
		return err
	}
	if a.qualified {
		return nil
	}
	return ValidateHarnessName(ref)
}

// ValidateNamespace validates a bundle namespace: the shared name rule plus a
// reserved set that a self-declared namespace may not claim — the clawker name
// exactly, any clawker-prefixed or -clawker-suffixed name, and the
// official-endorsement name. The reserved set blocks a distributed bundle from
// impersonating a first-party source.
func ValidateNamespace(ns string) error {
	if err := ValidateName(ns); err != nil {
		return err
	}
	switch {
	case ns == NamePrefix,
		ns == ReservedNamespaceOfficial,
		strings.HasPrefix(ns, NamePrefix+"-"),
		strings.HasSuffix(ns, "-"+NamePrefix):
		return fmt.Errorf("namespace %q is reserved", ns)
	}
	return nil
}
