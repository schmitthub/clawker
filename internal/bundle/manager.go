package bundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
	"github.com/schmitthub/clawker/internal/config"
)

// Manager is the command-facing facade over the bundle model: it owns a
// Resolver bound to one loaded config plus the git Fetcher, and adds the
// cache-mutating and validation operations the `clawker bundle` verbs and the
// bundle-consuming front doors (build/run/monitor up) need. Resolution and
// listing delegate to the Resolver; Install/Update/AutoUpdateCheck drive the
// fetch/cache pipeline; Remove purges a cached bundle; Validate is a local
// manifest check.
type Manager struct {
	cfg      config.Config
	resolver *Resolver
	fetcher  fetch.Fetcher
	// registeredRoots lists every registered project root (and worktree
	// path) whose declarations count as cache GC roots; nil disables GC
	// entirely (see WithRegisteredRoots).
	registeredRoots RegisteredRootsFn
	// validate loads a component through its consumption-time front door;
	// required at construction, applied by Validate and the install prefetch.
	validate ComponentValidator
}

// RegisteredRootsFn lists the project roots (including worktree paths) the
// host registry knows, so the bundle cache's GC roots can union every
// registered project's declarations — not just the current one's.
type RegisteredRootsFn func(ctx context.Context) ([]string, error)

// ComponentValidator loads one enumerated component through its
// consumption-time front door, returning the error the consuming command
// would surface. componentcheck.Validate is the production implementation;
// it is a required Manager dependency (not defaulted here) because the
// per-type loaders live in packages that import this one.
type ComponentValidator func(Component) error

// ManagerOption customizes a Manager at construction.
type ManagerOption func(*Manager)

// WithRegisteredRoots wires the registered-project-roots provider that makes
// cache GC possible. A Manager constructed without it never collects anything:
// AutoGC is a silent no-op and Prune refuses — fail-closed, since a roots
// union missing registered projects would collect entries they still declare.
func WithRegisteredRoots(fn RegisteredRootsFn) ManagerOption {
	return func(m *Manager) { m.registeredRoots = fn }
}

// NewManager constructs a Manager bound to cfg with the production git
// fetcher. validate must be non-nil (componentcheck.Validate in production) —
// every Manager surface that loads a bundle applies the same component check.
func NewManager(cfg config.Config, validate ComponentValidator, opts ...ManagerOption) *Manager {
	m := &Manager{
		cfg:             cfg,
		resolver:        NewResolver(cfg),
		fetcher:         fetch.NewFetcher(),
		registeredRoots: nil,
		validate:        validate,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
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
	// ComponentErrs are the hard failures from loading each enumerated
	// component through its consumption-time front door.
	ComponentErrs []error
}

// OK reports whether the bundle passes validation at the requested strictness.
// A load error or an invalid component always fails; under strict, any
// warning also fails.
func (r Report) OK(strict bool) bool {
	if r.LoadErr != nil || len(r.ComponentErrs) > 0 {
		return false
	}
	return !strict || len(r.Warnings) == 0
}

// Validate loads and validates the bundle directory at dir, collecting the
// hard-fail load error (if any), each component's consumption-time load
// failure, and the advisory warnings into a Report. It never touches the
// network — enumeration is a local filesystem walk. Strict elevation of
// warnings is the caller's decision (see Report.OK).
func (m *Manager) Validate(dir string) Report {
	b, err := LoadBundleDir(os.DirFS(dir), dir)
	if err != nil {
		return Report{Dir: dir, LoadErr: err, Warnings: nil, ComponentErrs: nil}
	}
	return Report{Dir: dir, LoadErr: nil, Warnings: b.Warnings, ComponentErrs: m.validateComponents(b)}
}

// validateComponents runs every enumerated component through the manager's
// consumption-time loader, collecting the failures. It is the single deep
// component check — Validate and the install prefetch both apply it.
func (m *Manager) validateComponents(b *Bundle) []error {
	var errs []error
	for _, c := range b.Components {
		if err := m.validate(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Remove purges every cache entry of a bundle identity — the whole
// <cacheRoot>/<namespace>/<name>/ tree, covering every declared-value key.
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

// Install fetches a declared bundle source into the host cache and returns the
// fetched identity (zero for a local in-place source, which is loaded directly
// from disk and never cached — Install is a no-op for it). A remote source is
// cloned, its manifest validated, and its content committed atomically into
// the value-keyed entry for the declared source
// (<cacheRoot>/<namespace>/<name>/<sourceKey>/). A fetch or validation failure
// leaves any previously cached entry untouched.
func (m *Manager) Install(ctx context.Context, src config.BundleSource) (BundleID, error) {
	s := SourceFromConfig(src)
	if s.IsLocal() {
		return BundleID{}, nil
	}
	id, _, err := m.fetchIntoCache(ctx, s)
	return id, err
}

// InstallDeclared fetches every declared-but-uncached remote bundle. It returns
// the identities freshly installed and the first error encountered (a failed
// source leaves earlier successes in place).
func (m *Manager) InstallDeclared(ctx context.Context) ([]BundleID, error) {
	cached, err := cachedKeys()
	if err != nil {
		return nil, err
	}
	var installed []BundleID
	for _, decl := range m.cfg.BundleDeclarations() {
		src := SourceFromConfig(decl.Source)
		if src.IsLocal() || cached[src.Key()] {
			continue
		}
		id, _, fetchErr := m.fetchIntoCache(ctx, src)
		if fetchErr != nil {
			return installed, fetchErr
		}
		installed = append(installed, id)
	}
	return installed, nil
}
