// Package stack loads file-backed stack definitions — named,
// self-guarded Dockerfile install fragments that projects and harness
// bundles DECLARE instead of hand-writing. A definition provisions a full
// language stack (e.g. node = baked LTS install in root scope + nvm
// setup in user scope) via up to two fragments, one per Dockerfile USER
// scope. Definitions resolve through a per-lineage lookup chain: a
// project stacks: registry entry in clawker.yaml, then (in a harness
// image) the selected bundle's stacks/ dir, then the definitions shipped
// embedded in internal/bundler as the virtual base layer — a closer layer
// wins wholesale. Resolution and stage placement live in internal/bundler;
// this package owns the definition format and its load-time validation.
package stack

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/consts"
)

// Definition is a loaded stack definition.
type Definition struct {
	// Name is the lookup key requirers declare.
	Name string
	// Description is the manifest's human summary of what the stack
	// provisions.
	Description string
	// RootFragment is the raw Dockerfile fragment rendered in a root-USER
	// region; empty when the definition ships no root fragment.
	RootFragment string
	// UserFragment is the raw Dockerfile fragment rendered in the
	// unprivileged-USER region; empty when the definition ships no user
	// fragment.
	UserFragment string
}

// manifest is the parsed stack.yaml.
type manifest struct {
	Description string `yaml:"description"`
}

// ValidateName rejects names that cannot serve as stack registry keys — a
// definition name is also a registry key, a directory name, and a token in
// build.stacks lists. Delegates to the unified naming rule shared by
// stacks, harnesses, and their registry/overlay keys (see
// consts.ValidateName).
func ValidateName(name string) error {
	if err := consts.ValidateName(name); err != nil {
		return fmt.Errorf("stack %w", err)
	}
	return nil
}

// Load reads a definition from fsys, whose root must be the definition
// directory (stack.yaml plus at least one fragment file). Use
// [os.DirFS] for on-disk registered or bundle-embedded definitions and a
// sub-FS of embedded assets for shipped ones. Every validation failure is a named
// error at this front door — never a silent render-time skip.
func Load(name string, fsys fs.FS) (*Definition, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	rawManifest, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("stack %q: read %s: %w", name, ManifestFile, err)
	}
	var m manifest
	if unmarshalErr := yaml.Unmarshal(rawManifest, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("stack %q: parse %s: %w", name, ManifestFile, unmarshalErr)
	}

	rootFragment, err := loadFragment(name, fsys, RootFragmentFile)
	if err != nil {
		return nil, err
	}
	userFragment, err := loadFragment(name, fsys, UserFragmentFile)
	if err != nil {
		return nil, err
	}
	if rootFragment == "" && userFragment == "" {
		return nil, fmt.Errorf(
			"stack %q: no fragment found — a definition ships %s, %s, or both",
			name, RootFragmentFile, UserFragmentFile,
		)
	}

	return &Definition{
		Name:         name,
		Description:  m.Description,
		RootFragment: rootFragment,
		UserFragment: userFragment,
	}, nil
}

// loadFragment reads and parse-checks one fragment file. A missing file is
// not an error (fragments are optional individually); an unreadable,
// empty, or syntactically invalid one is.
func loadFragment(name string, fsys fs.FS, file string) (string, error) {
	raw, err := fs.ReadFile(fsys, file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stack %q: read %s: %w", name, file, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("stack %q: %s is empty", name, file)
	}
	if _, parseErr := template.New(name).Parse(string(raw)); parseErr != nil {
		return "", fmt.Errorf("stack %q: parse %s: %w", name, file, parseErr)
	}
	return string(raw), nil
}
