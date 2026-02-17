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

// Registry provides access to project registry operations.
// Implemented by RegistryLoader (file-based) and test fakes.
type Registry interface {
	// Project returns a handle for operating on a specific project.
	Project(key string) ProjectHandle
	// Load reads and parses the registry file.
	Load() (*ProjectRegistry, error)
	// Save writes the registry to the file.
	Save(r *ProjectRegistry) error
	// Register adds a project to the registry, returns the slug key used.
	Register(displayName, rootDir string) (string, error)
	// Unregister removes a project from the registry by key.
	Unregister(key string) (bool, error)
	// UpdateProject atomically updates a project entry in the registry.
	UpdateProject(key string, fn func(*ProjectEntry) error) error
	// Path returns the full path to the registry file.
	Path() string
	// Exists checks if the registry file exists.
	Exists() bool
}

// ProjectHandle provides operations on a single project entry.
// Implemented by projectHandleImpl (file-based) and test fakes.
type ProjectHandle interface {
	// Key returns the project slug key.
	Key() string
	// Get returns the ProjectEntry, loading from disk if needed.
	Get() (*ProjectEntry, error)
	// Root returns the project root directory path.
	Root() (string, error)
	// Exists checks if the project exists in the registry.
	Exists() (bool, error)
	// Update atomically modifies the project entry.
	Update(fn func(*ProjectEntry) error) error
	// Delete removes the project from the registry.
	Delete() (bool, error)
	// Worktree returns a handle for operating on a specific worktree.
	Worktree(name string) WorktreeHandle
	// ListWorktrees returns handles for all worktrees in this project.
	ListWorktrees() ([]WorktreeHandle, error)
}

// WorktreeHandle provides operations and queries on a single worktree.
// Implemented by worktreeHandleImpl (file-based) and test fakes.
type WorktreeHandle interface {
	// Name returns the branch name that the worktree is tracking.
	Name() string
	// Slug returns the filesystem-safe slug.
	Slug() string
	// Path returns the worktree directory path.
	Path() (string, error)
	// DirExists returns true if the clawker-managed worktree directory exists
	// at ~/.local/clawker/projects/<key>/worktrees/<slug>.
	DirExists() bool
	// GitExists returns true if git worktree metadata is valid.
	// Reads <worktree-path>/.git file and validates it points to the correct location
	// in the project's .git/worktrees/<slug> directory.
	GitExists() bool
	// Status returns the health status by calling DirExists() and GitExists().
	Status() *WorktreeStatus
	// Delete removes the worktree entry from the registry.
	Delete() error
}

// ProjectEntry represents a registered project in the registry.
type ProjectEntry struct {
	Name      string            `yaml:"name"`
	Root      string            `yaml:"root"`
	Worktrees map[string]string `yaml:"worktrees,omitempty"`
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

// NewRegistryLoaderWithPath creates a RegistryLoader for a specific directory.
// This is used by tests and configtest that need to control the registry location.
// Note: No validation is performed; errors will occur at Load/Save time if the path is invalid.
func NewRegistryLoaderWithPath(dir string) *RegistryLoader {
	return &RegistryLoader{
		path: filepath.Join(dir, RegistryFileName),
	}
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

// Project returns a handle for operating on a specific project.
func (l *RegistryLoader) Project(key string) ProjectHandle {
	return &projectHandleImpl{
		loader: l,
		key:    key,
	}
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

// UpdateProject atomically updates a project entry in the registry.
// The updateFn receives a pointer to the entry which can be modified in place.
// If updateFn returns an error, the registry is not saved.
// Returns an error if the project is not found or if loading/saving fails.
func (l *RegistryLoader) UpdateProject(key string, updateFn func(*ProjectEntry) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	registry, err := l.Load()
	if err != nil {
		return err
	}

	entry, ok := registry.Projects[key]
	if !ok {
		return fmt.Errorf("project %q not found in registry", key)
	}

	if err := updateFn(&entry); err != nil {
		return err
	}

	registry.Projects[key] = entry
	return l.Save(registry)
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

// projectHandleImpl provides operations on a single project entry.
// It is a lightweight handle - actual data loaded on demand.
// Thread safety: mutations delegate to RegistryLoader which has a mutex.
// Implements the ProjectHandle interface.
type projectHandleImpl struct {
	loader *RegistryLoader
	key    string
}

// Key returns the project slug key.
func (p *projectHandleImpl) Key() string {
	return p.key
}

// Get returns the ProjectEntry, loading from disk if needed.
func (p *projectHandleImpl) Get() (*ProjectEntry, error) {
	registry, err := p.loader.Load()
	if err != nil {
		return nil, err
	}
	entry, ok := registry.Projects[p.key]
	if !ok {
		return nil, fmt.Errorf("project %q not found in registry", p.key)
	}
	return &entry, nil
}

// Root returns the project root directory path.
func (p *projectHandleImpl) Root() (string, error) {
	entry, err := p.Get()
	if err != nil {
		return "", err
	}
	return entry.Root, nil
}

// Exists checks if the project exists in the registry.
func (p *projectHandleImpl) Exists() (bool, error) {
	registry, err := p.loader.Load()
	if err != nil {
		return false, err
	}
	_, ok := registry.Projects[p.key]
	return ok, nil
}

// Update atomically modifies the project entry.
func (p *projectHandleImpl) Update(fn func(*ProjectEntry) error) error {
	return p.loader.UpdateProject(p.key, fn)
}

// Delete removes the project from the registry.
func (p *projectHandleImpl) Delete() (bool, error) {
	return p.loader.Unregister(p.key)
}

// Worktree returns a handle for operating on a specific worktree.
func (p *projectHandleImpl) Worktree(name string) WorktreeHandle {
	entry, err := p.Get()
	if err != nil {
		logger.Debug().Err(err).Str("project", p.key).Msg("failed to load project entry for worktree handle")
	}
	var slug string
	if entry != nil && entry.Worktrees != nil {
		slug = entry.Worktrees[name]
	}
	if slug == "" {
		slug = Slugify(name)
	}
	return &worktreeHandleImpl{
		project: p,
		name:    name,
		slug:    slug,
	}
}

// ListWorktrees returns handles for all worktrees in this project.
func (p *projectHandleImpl) ListWorktrees() ([]WorktreeHandle, error) {
	entry, err := p.Get()
	if err != nil {
		return nil, err
	}
	if entry.Worktrees == nil {
		return nil, nil
	}

	handles := make([]WorktreeHandle, 0, len(entry.Worktrees))
	for name, slug := range entry.Worktrees {
		handles = append(handles, &worktreeHandleImpl{
			project: p,
			name:    name,
			slug:    slug,
		})
	}
	return handles, nil
}

// worktreeHandleImpl provides operations and queries on a single worktree.
// Thread safety: mutations delegate to projectHandleImpl -> RegistryLoader.
// Implements the WorktreeHandle interface.
type worktreeHandleImpl struct {
	project *projectHandleImpl
	name    string
	slug    string
}

// Name returns the branch name that the worktree is tracking.
func (w *worktreeHandleImpl) Name() string {
	return w.name
}

// Slug returns the filesystem-safe slug.
func (w *worktreeHandleImpl) Slug() string {
	return w.slug
}

// Path returns the worktree directory path.
func (w *worktreeHandleImpl) Path() (string, error) {
	home, err := ClawkerHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "projects", w.project.Key(), "worktrees", w.slug), nil
}

// DirExists returns true if the worktree directory exists on disk.
func (w *worktreeHandleImpl) DirExists() bool {
	path, err := w.Path()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// GitExists returns true if git worktree metadata is valid.
// Reads <worktree-path>/.git file and validates it points to the correct location.
// Expected content: "gitdir: <project-root>/.git/worktrees/<slug>"
func (w *worktreeHandleImpl) GitExists() bool {
	wtPath, err := w.Path()
	if err != nil {
		return false
	}

	gitFile := filepath.Join(wtPath, ".git")
	content, err := os.ReadFile(gitFile)
	if err != nil {
		return false
	}

	// Parse "gitdir: <path>\n"
	line := strings.TrimSpace(string(content))
	if !strings.HasPrefix(line, "gitdir: ") {
		return false
	}
	actualPath := strings.TrimPrefix(line, "gitdir: ")

	// Validate it points to expected location
	projectRoot, err := w.project.Root()
	if err != nil {
		return false
	}
	expectedPath := filepath.Join(projectRoot, ".git", "worktrees", w.slug)

	return actualPath == expectedPath
}

// Status returns the health status by calling DirExists() and GitExists().
func (w *worktreeHandleImpl) Status() *WorktreeStatus {
	path, err := w.Path()
	return &WorktreeStatus{
		Name:      w.name,
		Slug:      w.slug,
		Path:      path,
		DirExists: w.DirExists(),
		GitExists: w.GitExists(),
		Error:     err,
	}
}

// Delete removes the worktree entry from the registry.
// Does NOT delete the directory or git metadata - use git.RemoveWorktree for full removal.
func (w *worktreeHandleImpl) Delete() error {
	return w.project.Update(func(entry *ProjectEntry) error {
		if entry.Worktrees == nil {
			return nil
		}
		delete(entry.Worktrees, w.name)
		return nil
	})
}

// WorktreeStatus holds the health check results for a worktree.
type WorktreeStatus struct {
	Name      string // Branch name that the worktree is tracking
	Slug      string // Filesystem-safe slug
	Path      string // Worktree directory path
	DirExists bool   // Worktree directory exists on filesystem
	GitExists bool   // .git file exists and points to valid git metadata
	Error     error  // Non-nil if path resolution failed
}

// IsPrunable returns true if registry entry exists but both dir and git are missing.
// Returns false if there was an error checking status (we can't safely prune if we don't know the state).
func (s *WorktreeStatus) IsPrunable() bool {
	return s.Error == nil && !s.DirExists && !s.GitExists
}

// IsHealthy returns true if both directory and git metadata exist.
func (s *WorktreeStatus) IsHealthy() bool {
	return s.DirExists && s.GitExists
}

// Issues returns a slice of issue descriptions, empty if healthy.
func (s *WorktreeStatus) Issues() []string {
	var issues []string
	if !s.DirExists {
		issues = append(issues, "dir missing")
	}
	if !s.GitExists {
		issues = append(issues, "git missing")
	}
	return issues
}

// String returns "healthy" when all checks pass, or comma-separated issues.
// If Error is non-nil, returns the error message.
func (s *WorktreeStatus) String() string {
	if s.Error != nil {
		return fmt.Sprintf("error: %v", s.Error)
	}
	if s.IsHealthy() {
		return "healthy"
	}
	return strings.Join(s.Issues(), ", ")
}
