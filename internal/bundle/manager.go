package bundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
)

// ErrNotWired is returned by the fetch-backed Manager operations (Install,
// Update) until the fetch/cache subsystem is built in a later phase. It is a
// typed sentinel so the command layer can present a clear "not yet available"
// message rather than a generic failure; callers match it with [errors.Is].
var ErrNotWired = errors.New("bundle fetch is not yet available in this build")

// Manager is the command-facing facade over the bundle model: it owns a
// Resolver bound to one loaded config and adds the cache-mutating and
// validation operations the `clawker bundle` verbs need. Resolution and
// listing delegate to the Resolver; Remove purges a cached bundle; Install and
// Update are the fetch-backed operations, stubbed until the fetch subsystem
// lands (they return ErrNotWired).
type Manager struct {
	cfg      config.Config
	resolver *Resolver
}

// NewManager constructs a Manager bound to cfg.
func NewManager(cfg config.Config) *Manager {
	return &Manager{cfg: cfg, resolver: NewResolver(cfg)}
}

// Resolver returns the manager's component resolver, used by listing surfaces
// that need Resolve/List/Bundles directly.
func (m *Manager) Resolver() *Resolver { return m.resolver }

// Declarations returns every declared bundle source paired with the config file
// that declared it, in layer order (highest priority first). It is the source
// of truth for "which bundles are declared" that listing and removal surfaces
// consult independently of what is cached.
func (m *Manager) Declarations() []config.BundleDeclaration {
	return m.cfg.BundleDeclarations()
}

// Report is the outcome of validating a bundle directory: the hard-fail load
// error (nil when the bundle loaded) and the advisory warnings a
// structurally-valid bundle accumulated. The command layer decides how strict
// to be — a plain validate passes with warnings, `--strict` treats them as
// failures.
type Report struct {
	// Dir is the validated bundle directory (display path).
	Dir string
	// LoadErr is the hard-fail manifest/enumeration error, or nil when the
	// bundle loaded successfully.
	LoadErr error
	// Warnings are the advisory findings (unknown dirs, empty convention dirs)
	// raised during a successful load.
	Warnings []Warning
}

// OK reports whether the bundle passes validation at the requested strictness.
// A load error always fails; under strict, any warning also fails.
func (r Report) OK(strict bool) bool {
	if r.LoadErr != nil {
		return false
	}
	return !strict || len(r.Warnings) == 0
}

// Validate loads and validates the bundle directory at dir, collecting the
// hard-fail load error (if any) and the advisory warnings into a Report. It
// never touches the network — enumeration is a local filesystem walk. Strict
// elevation of warnings is the caller's decision (see Report.OK).
func (m *Manager) Validate(dir string) Report {
	b, err := LoadBundleDir(os.DirFS(dir), dir)
	if err != nil {
		return Report{Dir: dir, LoadErr: err, Warnings: nil}
	}
	return Report{Dir: dir, LoadErr: nil, Warnings: b.Warnings}
}

// Remove purges a cached bundle's entire cache entry — every version content
// root plus the cache-internal metadata under <cacheRoot>/<namespace>/<name>/.
// It reports whether an entry existed to remove; a not-cached identity is a
// no-op returning false. It never reads or writes the declaring config — a
// still-declared bundle re-fetches on the next install (the caller warns).
func (m *Manager) Remove(id BundleID) (bool, error) {
	root, err := cacheRoot()
	if err != nil {
		return false, err
	}
	bundleDir := filepath.Join(root, id.Namespace, id.Name)
	if _, statErr := os.Stat(bundleDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, fmt.Errorf("stat bundle cache %s: %w", bundleDir, statErr)
	}
	if rmErr := os.RemoveAll(bundleDir); rmErr != nil {
		return false, fmt.Errorf("purge bundle cache %s: %w", bundleDir, rmErr)
	}
	return true, nil
}

// Install fetches a declared bundle source into the host cache. The fetch/cache
// subsystem is built in a later phase; until then it returns ErrNotWired so the
// install command can report that the declaration was written but content
// fetching is not yet available.
func (*Manager) Install(config.BundleSource) error { return ErrNotWired }

// Update re-fetches a cached bundle when its source version changed. Like
// Install it depends on the fetch subsystem and returns ErrNotWired until that
// lands.
func (*Manager) Update(BundleID) error { return ErrNotWired }
