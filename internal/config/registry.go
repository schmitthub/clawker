package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/schmitthub/clawker/internal/logger"
	"gopkg.in/yaml.v3"
)

const (
	// RegistryFileName is the name of the project registry file.
	RegistryFileName = "projects.yaml"
)

// ProjectEntry represents a registered project in the registry.
type ProjectEntry struct {
	Name string `yaml:"name"`
	Root string `yaml:"root"`
}

// Valid returns an error if the entry is missing required fields or has invalid values.
func (e ProjectEntry) Valid() error {
	if e.Name == "" {
		return fmt.Errorf("project entry has empty name")
	}
	if e.Root == "" {
		return fmt.Errorf("project entry has empty root path")
	}
	if !filepath.IsAbs(e.Root) {
		return fmt.Errorf("project entry root %q is not an absolute path", e.Root)
	}
	return nil
}

// ProjectRegistry holds the map of project slug keys to entries.
type ProjectRegistry struct {
	Projects map[string]ProjectEntry `yaml:"projects"`
}

// Lookup finds the registry entry whose Root matches workDir or is a parent of workDir.
// Uses longest prefix match when multiple entries could match.
// Returns the key and entry, or empty key if not found.
func (r *ProjectRegistry) Lookup(workDir string) (string, ProjectEntry) {
	if r == nil || len(r.Projects) == 0 {
		return "", ProjectEntry{}
	}

	absDir, err := filepath.Abs(workDir)
	if err != nil {
		logger.Debug().Err(err).Str("workDir", workDir).Msg("failed to resolve absolute path for registry lookup")
		return "", ProjectEntry{}
	}

	// Evaluate symlinks for consistent comparison
	absDir, err = filepath.EvalSymlinks(absDir)
	if err != nil {
		logger.Debug().Err(err).Str("path", absDir).Msg("failed to evaluate symlinks for work directory")
		// Fall back to the non-resolved path
		absDir, _ = filepath.Abs(workDir)
	}

	bestKey := ""
	bestEntry := ProjectEntry{}
	bestLen := 0

	for key, entry := range r.Projects {
		root := entry.Root
		// Evaluate symlinks on root too
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			logger.Debug().Err(err).Str("root", root).Msg("failed to evaluate symlinks for registry entry root")
			resolvedRoot = root
		}

		if absDir == resolvedRoot {
			// Exact match — best possible
			if len(resolvedRoot) > bestLen {
				bestKey = key
				bestEntry = entry
				bestLen = len(resolvedRoot)
			}
		} else if strings.HasPrefix(absDir, resolvedRoot+string(filepath.Separator)) {
			// Child directory — longest prefix wins
			if len(resolvedRoot) > bestLen {
				bestKey = key
				bestEntry = entry
				bestLen = len(resolvedRoot)
			}
		}
	}

	return bestKey, bestEntry
}

// LookupByKey returns the entry for the given key, or false if not found.
func (r *ProjectRegistry) LookupByKey(key string) (ProjectEntry, bool) {
	if r == nil || r.Projects == nil {
		return ProjectEntry{}, false
	}
	entry, ok := r.Projects[key]
	return entry, ok
}

// HasKey returns whether the registry contains the given key.
func (r *ProjectRegistry) HasKey(key string) bool {
	if r == nil || r.Projects == nil {
		return false
	}
	_, ok := r.Projects[key]
	return ok
}

// RegistryLoader handles loading and saving of the project registry.
type RegistryLoader struct {
	mu   sync.Mutex
	path string
}

// NewRegistryLoader creates a new RegistryLoader.
// It resolves the registry path from CLAWKER_HOME or the default location.
func NewRegistryLoader() (*RegistryLoader, error) {
	home, err := ClawkerHome()
	if err != nil {
		return nil, fmt.Errorf("failed to determine clawker home: %w", err)
	}
	return &RegistryLoader{
		path: filepath.Join(home, RegistryFileName),
	}, nil
}

// Path returns the full path to the registry file.
func (l *RegistryLoader) Path() string {
	return l.path
}

// Exists checks if the registry file exists.
func (l *RegistryLoader) Exists() bool {
	_, err := os.Stat(l.path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		logger.Debug().Err(err).Str("path", l.path).Msg("unexpected error checking registry file")
	}
	return false
}

// Load reads and parses the registry file.
// If the file doesn't exist, returns an empty registry (not an error).
func (l *RegistryLoader) Load() (*ProjectRegistry, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProjectRegistry{Projects: map[string]ProjectEntry{}}, nil
		}
		return nil, fmt.Errorf("failed to read registry file: %w", err)
	}

	var registry ProjectRegistry
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry file: %w", err)
	}

	// Ensure map is initialized
	if registry.Projects == nil {
		registry.Projects = map[string]ProjectEntry{}
	}

	// Validate entries (warn but don't remove — avoid destructive side effects on load)
	for key, entry := range registry.Projects {
		if err := entry.Valid(); err != nil {
			logger.Warn().Str("key", key).Err(err).Msg("invalid project entry in registry")
		}
	}

	return &registry, nil
}

// Save writes the registry to the file.
// Creates the parent directory if it doesn't exist.
func (l *RegistryLoader) Save(r *ProjectRegistry) error {
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create registry directory: %w", err)
	}

	data, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("failed to marshal registry: %w", err)
	}

	if err := os.WriteFile(l.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write registry file: %w", err)
	}

	return nil
}

// Register adds a project to the registry.
// The slug is derived from the display name. If a collision occurs, a suffix is appended.
// Returns the slug key used.
func (l *RegistryLoader) Register(displayName, rootDir string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry, err := l.Load()
	if err != nil {
		return "", err
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Check if this root is already registered
	for key, entry := range registry.Projects {
		resolvedExisting, err := filepath.EvalSymlinks(entry.Root)
		if err != nil {
			logger.Debug().Err(err).Str("root", entry.Root).Msg("failed to evaluate symlinks for existing registry entry")
			resolvedExisting = entry.Root
		}
		resolvedNew, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			logger.Debug().Err(err).Str("root", absRoot).Msg("failed to evaluate symlinks for new registry entry")
			resolvedNew = absRoot
		}
		if resolvedExisting == resolvedNew {
			// Already registered at this root, update name if different
			if entry.Name != displayName {
				entry.Name = displayName
				registry.Projects[key] = entry
				if err := l.Save(registry); err != nil {
					return "", err
				}
			}
			return key, nil
		}
	}

	// Generate unique slug
	existing := make(map[string]bool, len(registry.Projects))
	for k := range registry.Projects {
		existing[k] = true
	}
	slug := UniqueSlug(displayName, existing)

	registry.Projects[slug] = ProjectEntry{
		Name: displayName,
		Root: absRoot,
	}

	if err := l.Save(registry); err != nil {
		return "", err
	}

	return slug, nil
}

// Unregister removes a project from the registry by key.
// Returns true if the project was found and removed.
func (l *RegistryLoader) Unregister(key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry, err := l.Load()
	if err != nil {
		return false, err
	}

	if _, ok := registry.Projects[key]; !ok {
		return false, nil
	}

	delete(registry.Projects, key)

	if err := l.Save(registry); err != nil {
		return false, err
	}

	return true, nil
}

// slugRegexp matches characters that are NOT alphanumeric or hyphens.
var slugRegexp = regexp.MustCompile(`[^a-z0-9-]+`)

// leadingTrailingHyphens matches leading or trailing hyphens.
var leadingTrailingHyphens = regexp.MustCompile(`^-+|-+$`)

// multipleHyphens matches consecutive hyphens.
var multipleHyphens = regexp.MustCompile(`-{2,}`)

// Slugify converts a display name to a URL/filesystem-safe slug.
// "My Cool Project" → "my-cool-project"
func Slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRegexp.ReplaceAllString(s, "-")
	s = multipleHyphens.ReplaceAllString(s, "-")
	s = leadingTrailingHyphens.ReplaceAllString(s, "")

	if s == "" {
		return "project"
	}

	// Limit length to 64 chars
	if len(s) > 64 {
		s = s[:64]
		s = leadingTrailingHyphens.ReplaceAllString(s, "")
	}

	return s
}

// UniqueSlug returns a slug that doesn't collide with any key in existing.
// If "my-project" collides, tries "my-project-2", "my-project-3", etc.
func UniqueSlug(name string, existing map[string]bool) string {
	base := Slugify(name)
	if !existing[base] {
		return base
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !existing[candidate] {
			return candidate
		}
	}
}
