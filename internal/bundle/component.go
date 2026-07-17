// Package bundle owns the clawker bundle-install model: the three-tier
// component resolution (embedded floor, loose local dirs, installed bundles),
// bundle-directory loading, and the source/identity vocabulary. It is the layer
// between config (which owns the persisted file shapes — BundleSource,
// BundleManifest, and the harness/stack/monitoring component manifests) and the
// consumers that render or seed those components (internal/bundler for image
// generation, internal/monitor for observability). config never imports bundle;
// bundle imports config for the manifest shapes only.
package bundle

// ComponentType enumerates the three peer component kinds a bundle (or a loose
// convention dir, or the embedded floor) may ship. They are peers — a stack is
// never nested inside a harness; every tier enumerates all three the same way,
// by convention directory.
type ComponentType int

const (
	// ComponentHarness is a harness component (harnesses/<name>/harness.yaml).
	ComponentHarness ComponentType = iota
	// ComponentStack is a language-stack component (stacks/<name>/stack.yaml).
	ComponentStack
	// ComponentMonitoring is a monitoring-extension component
	// (monitoring/<name>/monitoring.yaml).
	ComponentMonitoring
)

// Convention directory names. Every tier — embedded floor, loose project/user
// dirs, and an installed bundle's content root — enumerates components from
// these directories; the subdirectory name IS the component name.
const (
	harnessesDir  = "harnesses"
	stacksDir     = "stacks"
	monitoringDir = "monitoring"
)

// Valid reports whether t is one of the three defined component types.
func (t ComponentType) Valid() bool {
	return t == ComponentHarness || t == ComponentStack || t == ComponentMonitoring
}

// Dir returns the convention subdirectory name for this component type — the
// directory that holds one <name>/ subdirectory per component of this type in
// every tier.
func (t ComponentType) Dir() string {
	switch t {
	case ComponentHarness:
		return harnessesDir
	case ComponentStack:
		return stacksDir
	case ComponentMonitoring:
		return monitoringDir
	default:
		return ""
	}
}

// String returns the singular human-readable name of the component type, used
// in provenance lines, errors, and warnings.
func (t ComponentType) String() string {
	switch t {
	case ComponentHarness:
		return "harness"
	case ComponentStack:
		return "stack"
	case ComponentMonitoring:
		// The convention dir name and the singular display name coincide.
		return monitoringDir
	default:
		return "unknown"
	}
}

// componentTypeForDir maps a convention directory name back to its component
// type. It is the reverse of Dir(), used by bundle enumeration to classify a
// top-level directory. The second return is false for any non-convention
// directory name (which the caller reports as an advisory unknown-dir warning).
func componentTypeForDir(dir string) (ComponentType, bool) {
	switch dir {
	case harnessesDir:
		return ComponentHarness, true
	case stacksDir:
		return ComponentStack, true
	case monitoringDir:
		return ComponentMonitoring, true
	default:
		return 0, false
	}
}
