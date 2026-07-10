package bundler

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// StackManifestFile is the manifest filename inside a stack definition
// directory.
const StackManifestFile = "stack.yaml"

// Fragment filenames inside a stack definition directory. A definition
// ships either or both; at least one must be present. The root fragment
// renders in a root-USER region of the generated Dockerfile, the user
// fragment in the unprivileged-USER region — one declaration can therefore
// provision a full language stack (e.g. node = root LTS install + user
// nvm setup).
const (
	StackRootFragmentFile = "Dockerfile.stack-root.tmpl"
	StackUserFragmentFile = "Dockerfile.stack-user.tmpl"
)

// StackDefinition is a loaded stack definition.
type StackDefinition struct {
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

// ValidateStackName validates a stack selection address: a bare name (embedded
// floor or a loose convention dir) or a qualified namespace.bundle.component
// address (installed bundle). It delegates to consts.ValidateComponentRef, the
// shared selection-key rule — a stack address appears in build.stacks lists, a
// harness manifest's stacks: dependency list, and as a loaded definition's
// name.
func ValidateStackName(name string) error {
	if err := consts.ValidateComponentRef(name); err != nil {
		return fmt.Errorf("stack %w", err)
	}
	return nil
}

// LoadStackDefinition reads a definition from fsys, whose root must be the
// definition directory (stack.yaml plus at least one fragment file). Use
// [os.DirFS] for on-disk registered or bundle-embedded definitions and a
// sub-FS of embedded assets for shipped ones. Every validation failure is a
// named error at this front door — never a silent render-time skip.
func LoadStackDefinition(name string, fsys fs.FS) (*StackDefinition, error) {
	if err := ValidateStackName(name); err != nil {
		return nil, err
	}

	rawManifest, err := fs.ReadFile(fsys, StackManifestFile)
	if err != nil {
		return nil, fmt.Errorf("stack %q: read %s: %w", name, StackManifestFile, err)
	}
	var m config.StackManifest
	if unmarshalErr := yaml.Unmarshal(rawManifest, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("stack %q: parse %s: %w", name, StackManifestFile, unmarshalErr)
	}

	rootFragment, err := loadFragment(name, fsys, StackRootFragmentFile)
	if err != nil {
		return nil, err
	}
	userFragment, err := loadFragment(name, fsys, StackUserFragmentFile)
	if err != nil {
		return nil, err
	}
	if rootFragment == "" && userFragment == "" {
		return nil, fmt.Errorf(
			"stack %q: no fragment found — a definition ships %s, %s, or both",
			name, StackRootFragmentFile, StackUserFragmentFile,
		)
	}

	return &StackDefinition{
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
