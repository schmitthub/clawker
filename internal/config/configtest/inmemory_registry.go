package configtest

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// WorktreeState holds the controllable state for a worktree.
type WorktreeState struct {
	DirExists   bool
	GitExists   bool
	DeleteError error // If non-nil, Delete() will return this error
}

// InMemoryRegistry implements config.Registry with in-memory storage.
// Useful for tests that don't need filesystem I/O.
// Worktree health status (DirExists/GitExists) is controllable via SetWorktreeState().
type InMemoryRegistry struct {
	mu            sync.Mutex
	registry      *config.ProjectRegistry
	worktreeState map[string]map[string]WorktreeState // project key -> worktree name -> state
}

// NewInMemoryRegistry creates a new in-memory registry.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		registry:      &config.ProjectRegistry{Projects: make(map[string]config.ProjectEntry)},
		worktreeState: make(map[string]map[string]WorktreeState),
	}
}

// SetWorktreeState configures DirExists/GitExists for a worktree.
// This controls what the WorktreeHandle.Status() will return.
func (r *InMemoryRegistry) SetWorktreeState(projectKey, worktreeName string, dirExists, gitExists bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.worktreeState[projectKey] == nil {
		r.worktreeState[projectKey] = make(map[string]WorktreeState)
	}
	r.worktreeState[projectKey][worktreeName] = WorktreeState{
		DirExists: dirExists,
		GitExists: gitExists,
	}
}

// SetWorktreeDeleteError configures Delete() to return an error for a worktree.
// Useful for testing partial failure scenarios in prune.
func (r *InMemoryRegistry) SetWorktreeDeleteError(projectKey, worktreeName string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.worktreeState[projectKey] == nil {
		r.worktreeState[projectKey] = make(map[string]WorktreeState)
	}
	state := r.worktreeState[projectKey][worktreeName]
	state.DeleteError = err
	r.worktreeState[projectKey][worktreeName] = state
}

// getWorktreeState returns the configured state for a worktree.
func (r *InMemoryRegistry) getWorktreeState(projectKey, worktreeName string) WorktreeState {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.worktreeState[projectKey] != nil {
		if state, ok := r.worktreeState[projectKey][worktreeName]; ok {
			return state
		}
	}
	// Default: both exist (healthy)
	return WorktreeState{DirExists: true, GitExists: true}
}

// --- Registry interface implementation ---

// Project returns a handle for operating on a specific project.
func (r *InMemoryRegistry) Project(key string) config.ProjectHandle {
	return &inMemoryProjectHandle{
		registry: r,
		key:      key,
	}
}

// Load returns the in-memory registry.
func (r *InMemoryRegistry) Load() (*config.ProjectRegistry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registry, nil
}

// Save stores the registry in memory.
func (r *InMemoryRegistry) Save(reg *config.ProjectRegistry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registry = reg
	return nil
}

// Register adds a project to the registry.
func (r *InMemoryRegistry) Register(displayName, rootDir string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate unique slug
	existing := make(map[string]bool, len(r.registry.Projects))
	for k := range r.registry.Projects {
		existing[k] = true
	}
	slug := config.UniqueSlug(displayName, existing)

	r.registry.Projects[slug] = config.ProjectEntry{
		Name: displayName,
		Root: rootDir,
	}
	return slug, nil
}

// Unregister removes a project from the registry by key.
func (r *InMemoryRegistry) Unregister(key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.registry.Projects[key]; !ok {
		return false, nil
	}
	delete(r.registry.Projects, key)
	return true, nil
}

// UpdateProject atomically updates a project entry in the registry.
func (r *InMemoryRegistry) UpdateProject(key string, fn func(*config.ProjectEntry) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.registry.Projects[key]
	if !ok {
		return fmt.Errorf("project %q not found in registry", key)
	}

	if err := fn(&entry); err != nil {
		return err
	}

	r.registry.Projects[key] = entry
	return nil
}

// Path returns an empty path (in-memory has no file).
func (r *InMemoryRegistry) Path() string {
	return ""
}

// Exists always returns true for in-memory registry.
func (r *InMemoryRegistry) Exists() bool {
	return true
}

// --- inMemoryProjectHandle implements config.ProjectHandle ---

type inMemoryProjectHandle struct {
	registry *InMemoryRegistry
	key      string
}

func (p *inMemoryProjectHandle) Key() string {
	return p.key
}

func (p *inMemoryProjectHandle) Get() (*config.ProjectEntry, error) {
	registry, err := p.registry.Load()
	if err != nil {
		return nil, err
	}
	entry, ok := registry.Projects[p.key]
	if !ok {
		return nil, fmt.Errorf("project %q not found in registry", p.key)
	}
	return &entry, nil
}

func (p *inMemoryProjectHandle) Root() (string, error) {
	entry, err := p.Get()
	if err != nil {
		return "", err
	}
	return entry.Root, nil
}

func (p *inMemoryProjectHandle) Exists() (bool, error) {
	registry, err := p.registry.Load()
	if err != nil {
		return false, err
	}
	_, ok := registry.Projects[p.key]
	return ok, nil
}

func (p *inMemoryProjectHandle) Update(fn func(*config.ProjectEntry) error) error {
	return p.registry.UpdateProject(p.key, fn)
}

func (p *inMemoryProjectHandle) Delete() (bool, error) {
	return p.registry.Unregister(p.key)
}

func (p *inMemoryProjectHandle) Worktree(name string) config.WorktreeHandle {
	entry, err := p.Get()
	if err != nil {
		logger.Debug().Err(err).Str("project", p.key).Msg("failed to load project entry for worktree handle")
	}
	var slug string
	if entry != nil && entry.Worktrees != nil {
		slug = entry.Worktrees[name]
	}
	if slug == "" {
		slug = config.Slugify(name)
	}
	return &inMemoryWorktreeHandle{
		registry:   p.registry,
		projectKey: p.key,
		name:       name,
		slug:       slug,
	}
}

func (p *inMemoryProjectHandle) ListWorktrees() ([]config.WorktreeHandle, error) {
	entry, err := p.Get()
	if err != nil {
		return nil, err
	}
	if entry.Worktrees == nil {
		return nil, nil
	}

	handles := make([]config.WorktreeHandle, 0, len(entry.Worktrees))
	for name, slug := range entry.Worktrees {
		handles = append(handles, &inMemoryWorktreeHandle{
			registry:   p.registry,
			projectKey: p.key,
			name:       name,
			slug:       slug,
		})
	}
	return handles, nil
}

// --- inMemoryWorktreeHandle implements config.WorktreeHandle ---

type inMemoryWorktreeHandle struct {
	registry   *InMemoryRegistry
	projectKey string
	name       string
	slug       string
}

func (w *inMemoryWorktreeHandle) Name() string {
	return w.name
}

func (w *inMemoryWorktreeHandle) Slug() string {
	return w.slug
}

func (w *inMemoryWorktreeHandle) Path() (string, error) {
	// Return a fake path for testing
	return filepath.Join("/fake", "projects", w.projectKey, "worktrees", w.slug), nil
}

func (w *inMemoryWorktreeHandle) DirExists() bool {
	state := w.registry.getWorktreeState(w.projectKey, w.name)
	return state.DirExists
}

func (w *inMemoryWorktreeHandle) GitExists() bool {
	state := w.registry.getWorktreeState(w.projectKey, w.name)
	return state.GitExists
}

func (w *inMemoryWorktreeHandle) Status() *config.WorktreeStatus {
	path, err := w.Path()
	return &config.WorktreeStatus{
		Name:      w.name,
		Slug:      w.slug,
		Path:      path,
		DirExists: w.DirExists(),
		GitExists: w.GitExists(),
		Error:     err,
	}
}

func (w *inMemoryWorktreeHandle) Delete() error {
	// Check if a delete error was configured for testing
	state := w.registry.getWorktreeState(w.projectKey, w.name)
	if state.DeleteError != nil {
		return state.DeleteError
	}
	return w.registry.UpdateProject(w.projectKey, func(entry *config.ProjectEntry) error {
		if entry.Worktrees == nil {
			return nil
		}
		delete(entry.Worktrees, w.name)
		return nil
	})
}

// --- InMemoryRegistryBuilder for fluent test setup ---

// InMemoryRegistryBuilder provides a fluent API for building test registries.
type InMemoryRegistryBuilder struct {
	registry *InMemoryRegistry
}

// NewInMemoryRegistryBuilder creates a new builder.
func NewInMemoryRegistryBuilder() *InMemoryRegistryBuilder {
	return &InMemoryRegistryBuilder{
		registry: NewInMemoryRegistry(),
	}
}

// WithProject adds a project and returns a project builder for further configuration.
func (b *InMemoryRegistryBuilder) WithProject(key, name, root string) *InMemoryProjectBuilder {
	b.registry.mu.Lock()
	b.registry.registry.Projects[key] = config.ProjectEntry{
		Name:      name,
		Root:      root,
		Worktrees: make(map[string]string),
	}
	b.registry.mu.Unlock()

	return &InMemoryProjectBuilder{
		parent: b,
		key:    key,
	}
}

// Build returns the configured registry.
func (b *InMemoryRegistryBuilder) Build() config.Registry {
	return b.registry
}

// InMemoryProjectBuilder provides a fluent API for adding worktrees to a project.
type InMemoryProjectBuilder struct {
	parent *InMemoryRegistryBuilder
	key    string
}

// WithWorktree adds a worktree entry to the project (default: healthy).
func (pb *InMemoryProjectBuilder) WithWorktree(name, slug string) *InMemoryProjectBuilder {
	pb.parent.registry.mu.Lock()
	entry := pb.parent.registry.registry.Projects[pb.key]
	if entry.Worktrees == nil {
		entry.Worktrees = make(map[string]string)
	}
	entry.Worktrees[name] = slug
	pb.parent.registry.registry.Projects[pb.key] = entry
	pb.parent.registry.mu.Unlock()

	// Default: healthy
	pb.parent.registry.SetWorktreeState(pb.key, name, true, true)
	return pb
}

// WithHealthyWorktree adds a worktree with DirExists=true, GitExists=true.
func (pb *InMemoryProjectBuilder) WithHealthyWorktree(name, slug string) *InMemoryProjectBuilder {
	pb.WithWorktree(name, slug)
	pb.parent.registry.SetWorktreeState(pb.key, name, true, true)
	return pb
}

// WithStaleWorktree adds a worktree with DirExists=false, GitExists=false.
func (pb *InMemoryProjectBuilder) WithStaleWorktree(name, slug string) *InMemoryProjectBuilder {
	pb.WithWorktree(name, slug)
	pb.parent.registry.SetWorktreeState(pb.key, name, false, false)
	return pb
}

// WithPartialWorktree adds a worktree with custom DirExists/GitExists values.
func (pb *InMemoryProjectBuilder) WithPartialWorktree(name, slug string, dirExists, gitExists bool) *InMemoryProjectBuilder {
	pb.WithWorktree(name, slug)
	pb.parent.registry.SetWorktreeState(pb.key, name, dirExists, gitExists)
	return pb
}

// Registry returns to the parent builder for chaining.
func (pb *InMemoryProjectBuilder) Registry() *InMemoryRegistryBuilder {
	return pb.parent
}
