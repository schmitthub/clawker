package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// MarkerDir is the hidden directory that marks a repository (sub)tree as a
// distributed bundle and holds its manifest.
const MarkerDir = ".clawker-bundle"

// ManifestFile is the bundle manifest filename inside MarkerDir.
const ManifestFile = "bundle.yaml"

// Component is one enumerated component of a bundle (or a floor/loose tier): its
// address, type, a filesystem rooted at its directory, the resolved directory
// path (for provenance), and — once a resolver has placed it — its provenance.
type Component struct {
	// Address is the component's address: bare for floor/loose, qualified
	// (namespace.bundle.name) for installed/in-place bundle components.
	Address Address
	// Type is the component kind.
	Type ComponentType
	// FS is a filesystem rooted at the component's own directory, ready for the
	// type's loader.
	FS fs.FS
	// Dir is the resolved directory path (on-disk or embedded), used in
	// provenance and errors.
	Dir string
	// Provenance records the resolution tier and any shadowed components; it is
	// stamped by the resolver, left zero by LoadBundleDir.
	Provenance Provenance
}

// Bundle is a loaded bundle directory: its identity from the manifest, the
// parsed manifest, the resolved directory path, the components enumerated by
// convention directory, and any advisory warnings raised during enumeration.
type Bundle struct {
	ID         BundleID
	Manifest   config.BundleManifest
	Dir        string
	Components []Component
	Warnings   []Warning
}

// Component returns the enumerated component of the given type and name, if the
// bundle ships one.
func (b *Bundle) Component(t ComponentType, name string) (Component, bool) {
	for _, c := range b.Components {
		if c.Type == t && c.Address.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// LoadBundleDir loads a bundle from fsys, whose root is the bundle directory
// (the one containing the MarkerDir). displayDir is the human-facing path used
// in warnings, provenance, and errors (an absolute on-disk path for cache /
// in-place bundles). It parses the manifest and enumerates components purely by
// convention directory — it does NOT parse component manifests (each component's
// own loader does that). A malformed or invalid manifest is a hard ManifestError;
// unknown top-level directories and empty convention directories are advisory
// Warnings on the returned bundle, never load failures.
func LoadBundleDir(fsys fs.FS, displayDir string) (*Bundle, error) {
	manifest, err := loadBundleManifest(fsys, displayDir)
	if err != nil {
		return nil, err
	}

	id := BundleID{Namespace: manifest.Namespace, Name: manifest.Name}
	b := &Bundle{ID: id, Manifest: manifest, Dir: displayDir, Components: nil, Warnings: nil}

	warnings, components, err := enumerateComponents(fsys, displayDir, id)
	if err != nil {
		return nil, &ManifestError{Dir: displayDir, Err: err}
	}
	b.Components = components
	b.Warnings = warnings
	return b, nil
}

// loadBundleManifest reads and validates .clawker-bundle/bundle.yaml. Every
// failure is a ManifestError — the hard-fail half of the validation split.
func loadBundleManifest(fsys fs.FS, displayDir string) (config.BundleManifest, error) {
	manifestPath := path.Join(MarkerDir, ManifestFile)
	raw, err := fs.ReadFile(fsys, manifestPath)
	if err != nil {
		return config.BundleManifest{}, &ManifestError{
			Dir: displayDir,
			Err: fmt.Errorf("read %s: %w", manifestPath, err),
		}
	}

	var m config.BundleManifest
	if unmarshalErr := yaml.Unmarshal(raw, &m); unmarshalErr != nil {
		return config.BundleManifest{}, &ManifestError{
			Dir: displayDir,
			Err: fmt.Errorf("parse %s: %w", manifestPath, unmarshalErr),
		}
	}

	if m.Namespace == "" {
		return config.BundleManifest{}, &ManifestError{
			Dir: displayDir,
			Err: errors.New("manifest is missing the required namespace field"),
		}
	}
	if nsErr := consts.ValidateNamespace(m.Namespace); nsErr != nil {
		return config.BundleManifest{}, &ManifestError{Dir: displayDir, Err: nsErr}
	}
	if m.Name == "" {
		return config.BundleManifest{}, &ManifestError{
			Dir: displayDir,
			Err: errors.New("manifest is missing the required name field"),
		}
	}
	if nameErr := consts.ValidateName(m.Name); nameErr != nil {
		return config.BundleManifest{}, &ManifestError{Dir: displayDir, Err: nameErr}
	}
	return m, nil
}

// enumerateComponents walks the bundle's top-level directories, classifying each
// as a convention directory (harnesses/stacks/monitoring — enumerated into
// components) or an unknown directory (advisory warning). Empty convention
// directories warn. Component names inside a convention directory are validated
// against the shared name rule; a bad name is a hard error (returned to the
// caller as a ManifestError).
func enumerateComponents(fsys fs.FS, displayDir string, id BundleID) ([]Warning, []Component, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("read bundle root: %w", err)
	}

	var warnings []Warning
	var components []Component
	for _, e := range entries {
		if skipTopLevelEntry(e) {
			continue
		}
		t, ok := componentTypeForDir(e.Name())
		if !ok {
			warnings = append(warnings, unknownDirWarning(e.Name()))
			continue
		}
		typeComponents, typeWarnings, compErr := enumerateComponentType(fsys, displayDir, id, t)
		if compErr != nil {
			return nil, nil, compErr
		}
		components = append(components, typeComponents...)
		warnings = append(warnings, typeWarnings...)
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i].Type != components[j].Type {
			return components[i].Type < components[j].Type
		}
		return components[i].Address.Name < components[j].Address.Name
	})
	return warnings, components, nil
}

// skipTopLevelEntry reports whether a bundle-root entry is outside enumeration:
// stray files (README, LICENSE, …) are unremarkable, the marker dir is the
// manifest's home, and dot-prefixed directories (.git, .github, …) are
// repository plumbing expected in an in-place dev-loop bundle — never worth an
// unknown-dir warning. The cache scan skips dot entries the same way.
func skipTopLevelEntry(e fs.DirEntry) bool {
	return !e.IsDir() || e.Name() == MarkerDir || strings.HasPrefix(e.Name(), ".")
}

// enumerateComponentType enumerates one convention directory into components.
func enumerateComponentType(
	fsys fs.FS, displayDir string, id BundleID, t ComponentType,
) ([]Component, []Warning, error) {
	entries, err := fs.ReadDir(fsys, t.Dir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s/: %w", t.Dir(), err)
	}

	var components []Component
	for _, e := range entries {
		if !e.IsDir() {
			// A stray file beside component dirs (a README, a .DS_Store) is
			// harmless — enumeration is directory-driven. Hard-failing here
			// would let one innocent file block the whole bundle set.
			continue
		}
		if nameErr := consts.ValidateName(e.Name()); nameErr != nil {
			return nil, nil, fmt.Errorf("%s component: %w", t, nameErr)
		}
		compDir := path.Join(t.Dir(), e.Name())
		sub, subErr := fs.Sub(fsys, compDir)
		if subErr != nil {
			return nil, nil, fmt.Errorf("%s %q: %w", t, e.Name(), subErr)
		}
		components = append(components, Component{
			Address:    Address{Namespace: id.Namespace, Bundle: id.Name, Name: e.Name()},
			Type:       t,
			FS:         sub,
			Dir:        path.Join(displayDir, compDir),
			Provenance: Provenance{}, //nolint:exhaustruct // stamped by the resolver, zero from the loader by contract
		})
	}
	if len(components) == 0 {
		return nil, []Warning{emptyComponentDirWarning(t)}, nil
	}
	return components, nil, nil
}
