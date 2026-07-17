package bundle

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/consts"
)

// BundleID is a bundle's identity — the (namespace, name) pair drawn
// exclusively from its manifest. It keys the installed-bundle cache
// (<data>/bundles/<namespace>/<name>/), identity-collision detection (C1), and
// the namespace segment of every component address the bundle contributes.
// Nothing derived from a source URL is ever identity-bearing.
type BundleID struct {
	Namespace string
	Name      string
}

// String renders the identity as namespace.name — the same dotted spelling the
// bundle-level CLI surfaces accept (bundle remove/update take <namespace.name>),
// so errors and listings print exactly what a user would type back. Cache
// directory layout uses [path/filepath.Join] over the pair separately; this is never a
// path.
func (id BundleID) String() string {
	return consts.JoinIdentity(id.Namespace, id.Name)
}

// zero reports whether the identity is unset — the "update all" sentinel for a
// no-argument bundle update.
func (id BundleID) zero() bool {
	return id.Namespace == "" && id.Name == ""
}

// Address is a resolved component address. A bare address (Namespace == "")
// names a floor or loose-tier component by its lone name; a qualified address
// carries all three segments (namespace.bundle.component) and names an
// installed-bundle component. Namespace and Bundle are set together or not at
// all.
type Address struct {
	Namespace string
	Bundle    string
	Name      string
}

// BareAddress builds a bare (floor/loose) component address from a lone name.
func BareAddress(name string) Address {
	return Address{Namespace: "", Bundle: "", Name: name}
}

// Qualified reports whether the address names installed-bundle content (carries
// a namespace and bundle) rather than a bare floor/loose component.
func (a Address) Qualified() bool {
	return a.Namespace != ""
}

// String renders the address in the one spelling used on every surface: a bare
// name for floor/loose components, or the dotted namespace.bundle.component
// triple for installed-bundle components (via consts.JoinAddress, never a
// hardcoded separator).
func (a Address) String() string {
	if a.Qualified() {
		return consts.JoinAddress(a.Namespace, a.Bundle, a.Name)
	}
	return a.Name
}

// ID returns the identity of the bundle a qualified address belongs to. It is
// meaningful only when Qualified() is true; a bare address yields the zero
// BundleID.
func (a Address) ID() BundleID {
	return BundleID{Namespace: a.Namespace, Name: a.Bundle}
}

// ParseAddress classifies s as a bare name or a fully-qualified
// namespace.bundle.component address, validating every segment against the
// shared name rule. It delegates to consts.SplitAddress; the reserved-namespace
// gate is applied at the bundle manifest front door, not here.
func ParseAddress(s string) (Address, error) {
	ns, bundleName, name, _, err := consts.SplitAddress(s)
	if err != nil {
		return Address{}, fmt.Errorf("component address: %w", err)
	}
	return Address{Namespace: ns, Bundle: bundleName, Name: name}, nil
}
