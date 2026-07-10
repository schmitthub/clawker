package bundle

import (
	"fmt"
	"strings"
)

// Tier is the resolution tier a component came from, in precedence order: a
// loose user dir shadows a loose project dir shadows the embedded floor; an
// installed or in-place bundle is addressed separately (qualified names) and
// never shadows the bare tiers.
type Tier int

const (
	// TierFloor is the embedded, in-binary shipped component (bare name).
	TierFloor Tier = iota
	// TierLooseProject is a component in the project's .clawker convention dir.
	TierLooseProject
	// TierLooseUser is a component in the user config-dir convention dir.
	TierLooseUser
	// TierInstalled is a component from a fetched bundle in the host cache
	// (qualified name).
	TierInstalled
	// TierInPlace is a component from a local in-place bundle source loaded
	// directly from disk (qualified name, the dev loop).
	TierInPlace
)

// Label returns the human-readable provenance label for the tier, used in
// listings and build output.
func (t Tier) Label() string {
	switch t {
	case TierFloor:
		return "built-in"
	case TierLooseProject:
		return "project"
	case TierLooseUser:
		return "user"
	case TierInstalled:
		return "bundle"
	case TierInPlace:
		return "bundle (in place)"
	default:
		return "unknown"
	}
}

// Provenance records where a resolved component came from and what same-named
// components it shadowed at farther tiers. Bare-name resolution shadows across
// the floor/loose tiers (C3/C4); qualified resolution never shadows.
type Provenance struct {
	// Tier is the tier the component resolved from.
	Tier Tier
	// Dir is the resolved on-disk directory (or embedded path) of the component.
	Dir string
	// Bundle is the identity of the owning bundle for installed/in-place tiers;
	// the zero BundleID for floor/loose tiers.
	Bundle BundleID
	// Shadows lists the farther-tier resolutions this one shadowed, closest
	// first — populated only when a closer tier won over a farther one.
	Shadows []Provenance
}

// Shadowed reports whether this resolution shadowed a farther-tier component of
// the same name.
func (p Provenance) Shadowed() bool {
	return len(p.Shadows) > 0
}

// Source returns the provenance source clause for one tier — its label plus, for
// bundle tiers, the owning identity, and for loose tiers, the directory.
func (p Provenance) Source() string {
	switch p.Tier {
	case TierInstalled, TierInPlace:
		return fmt.Sprintf("%s %s", p.Tier.Label(), p.Bundle)
	case TierLooseProject, TierLooseUser:
		return fmt.Sprintf("%s (%s)", p.Tier.Label(), p.Dir)
	case TierFloor:
		return p.Tier.Label()
	default:
		return p.Tier.Label()
	}
}

// Line renders a single provenance line for the named component: its resolved
// source and, when it shadowed farther tiers, the shadowed sources.
func (p Provenance) Line(name string) string {
	if !p.Shadowed() {
		return fmt.Sprintf("%s ← %s", name, p.Source())
	}
	sources := make([]string, 0, len(p.Shadows))
	for _, s := range p.Shadows {
		sources = append(sources, s.Source())
	}
	return fmt.Sprintf("%s ← %s shadows %s", name, p.Source(), strings.Join(sources, ", "))
}
