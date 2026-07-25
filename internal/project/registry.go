package project

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// Registry is the project registry domain facade — the single owner of registry
// persistence (consts.RegistryFile in the data dir) and project-root
// resolution. Construct one per process via NewRegistry and inject it; the CLI
// factory exposes it as f.ProjectRegistry. Nothing else constructs registry
// storage.
//
// The interface carries the registry's exported domain verbs; the unexported
// implementation embeds *storage.Store[ProjectRegistry], so the promoted
// engine verbs stay reachable on the concrete type as the escape hatch for
// edge cases. Consumers outside this package normally mutate registry state
// through ProjectManager, which wraps these verbs with project-lifecycle
// concerns.
//
// Every write verb owns the single schema key (keyProjects): it stages the new
// entry list with Set and persists it with Write in one call, so no caller ever
// holds a staged-but-unwritten registry.
//
//go:generate moq -rm -pkg mocks -out mocks/registry_mock.go . Registry
type Registry interface {
	// ResolveRoot returns the deepest registered project root that is an
	// ancestor of cwd, expressed in cwd's own path form. It returns
	// ErrNotInProject when cwd is not within any registered project root.
	ResolveRoot(cwd string) (string, error)
	// CurrentRoot resolves the project root for the process working directory,
	// propagating ErrNotInProject from ResolveRoot.
	CurrentRoot() (string, error)

	// Projects returns the registered project entries. An absent key is the
	// empty registry, not an error.
	Projects() ([]ProjectEntry, error)
	// ProjectByRoot returns the entry whose root identifies the same directory
	// as root, and whether one was found.
	ProjectByRoot(root string) (ProjectEntry, bool, error)
	// Register adds a project by root path and persists the registry.
	Register(displayName, rootDir string) (ProjectEntry, error)
	// Update replaces an existing entry by root identity and persists.
	Update(entry ProjectEntry) (ProjectEntry, error)
	// RemoveByRoot drops the entry identifying root and persists.
	RemoveByRoot(root string) error
	// RegisterWorktree records a worktree on its project entry and persists.
	RegisterWorktree(projectRoot, branch, path string) error
	// UnregisterWorktree drops a worktree from its project entry and persists.
	UnregisterWorktree(projectRoot, branch string) error
}

// registryImpl is the storage-backed implementation of Registry. It embeds
// *storage.Store[ProjectRegistry] so the engine verbs stay reachable as the
// escape hatch; those promoted methods never leak past the Registry interface,
// since the type is unexported and only ever handed out as the interface (the
// canonical store-backed pattern — see .claude/rules/store-backed-package.md).
type registryImpl struct {
	*storage.Store[ProjectRegistry]
}

// NewRegistry creates the file-backed project registry facade. The constructor
// IS the load: discovery, merge, and the strict schema decode all run here, so
// an unreadable or malformed registry file surfaces as an error from
// NewRegistry rather than as a silent empty registry at first read. All option
// wiring lives here, once.
//
//nolint:ireturn // returns the Registry domain interface by design — registryImpl stays package-private
func NewRegistry() (Registry, error) {
	store, err := storage.New[ProjectRegistry](
		storage.WithFilenames(consts.RegistryFile),
		storage.WithDefaultFilename(consts.RegistryFile),
		storage.WithDataDir(),
		storage.WithLock(),
	)
	if err != nil {
		return nil, fmt.Errorf("project: loading registry: %w", err)
	}
	return &registryImpl{Store: store}, nil
}

// NewRegistryFromString is the in-memory seam: the seed YAML is the only
// layer, parsed through the real schema with no directory, no discovery, and
// no disk. It deliberately omits every path option so it can never read or
// write a file — that is the whole point. Tests that need seeded registry
// content without an isolated filesystem use it; the file-backed path is
// covered by NewRegistry plus testenv.
//
//nolint:ireturn // returns the Registry domain interface by design — registryImpl stays package-private
func NewRegistryFromString(seed string) (Registry, error) {
	store, err := storage.NewFromString[ProjectRegistry](seed)
	if err != nil {
		return nil, fmt.Errorf("project: loading registry from string: %w", err)
	}
	return &registryImpl{Store: store}, nil
}

// Projects returns the registered project entries. Each call decodes a fresh
// value out of the merged tree, so callers may mutate the result (including
// each entry's Worktrees map) without touching live store state — mutations
// reach the store only through a write verb. An absent key is the empty
// registry, not an error.
func (r *registryImpl) Projects() ([]ProjectEntry, error) {
	entries, err := storage.Get[[]ProjectEntry](r.Store, keyProjects)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: reading registry projects: %w", err)
	}
	return entries, nil
}

// indexOfRoot returns the position of the entry whose root identifies the same
// directory as root, or -1. Both sides go through resolveRootPath so a root
// recorded through a symlink matches its real path and vice versa.
func indexOfRoot(entries []ProjectEntry, root string) int {
	resolved := resolveRootPath(root)
	for i, entry := range entries {
		if resolveRootPath(entry.Root) == resolved {
			return i
		}
	}
	return -1
}

func (r *registryImpl) ProjectByRoot(root string) (ProjectEntry, bool, error) {
	entries, err := r.Projects()
	if err != nil {
		return ProjectEntry{}, false, err
	}
	index := indexOfRoot(entries, root)
	if index < 0 {
		return ProjectEntry{}, false, nil
	}
	return entries[index], true, nil
}

func (r *registryImpl) RemoveByRoot(root string) error {
	entries, err := r.Projects()
	if err != nil {
		return err
	}
	index := indexOfRoot(entries, root)
	if index < 0 {
		return ErrProjectNotFound
	}

	if err = r.Set([]string{keyProjects}, append(entries[:index], entries[index+1:]...)); err != nil {
		return fmt.Errorf("project: removing registry entry %q: %w", root, err)
	}
	if err = r.Write(); err != nil {
		return fmt.Errorf("project: removing registry entry %q: %w", root, err)
	}
	return nil
}

func (r *registryImpl) RegisterWorktree(projectRoot, branch, path string) error {
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	entries, err := r.Projects()
	if err != nil {
		return err
	}
	index := indexOfRoot(entries, projectRoot)
	if index < 0 {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}

	if entries[index].Worktrees == nil {
		entries[index].Worktrees = map[string]WorktreeEntry{}
	}
	entries[index].Worktrees[branch] = WorktreeEntry{Path: path, Branch: branch}

	if err = r.Set([]string{keyProjects}, entries); err != nil {
		return fmt.Errorf("project: registering worktree %q: %w", branch, err)
	}
	if err = r.Write(); err != nil {
		return fmt.Errorf("project: registering worktree %q: %w", branch, err)
	}
	return nil
}

func (r *registryImpl) UnregisterWorktree(projectRoot, branch string) error {
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	entries, err := r.Projects()
	if err != nil {
		return err
	}
	index := indexOfRoot(entries, projectRoot)
	if index < 0 {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}
	if len(entries[index].Worktrees) == 0 {
		return nil
	}

	delete(entries[index].Worktrees, branch)

	if err = r.Set([]string{keyProjects}, entries); err != nil {
		return fmt.Errorf("project: unregistering worktree %q: %w", branch, err)
	}
	if err = r.Write(); err != nil {
		return fmt.Errorf("project: unregistering worktree %q: %w", branch, err)
	}
	return nil
}

// Register adds a project by root path.
func (r *registryImpl) Register(displayName, rootDir string) (ProjectEntry, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return ProjectEntry{}, fmt.Errorf("project: resolving absolute path for %q: %w", rootDir, err)
	}

	entries, err := r.Projects()
	if err != nil {
		return ProjectEntry{}, err
	}
	if indexOfRoot(entries, absRoot) >= 0 {
		return ProjectEntry{}, ErrProjectExists
	}

	entry := ProjectEntry{Name: displayName, Root: absRoot}
	if err = r.Set([]string{keyProjects}, append(entries, entry)); err != nil {
		return ProjectEntry{}, fmt.Errorf("project: registering project %q: %w", absRoot, err)
	}
	if err = r.Write(); err != nil {
		return ProjectEntry{}, fmt.Errorf("project: registering project %q: %w", absRoot, err)
	}

	return entry, nil
}

func (r *registryImpl) Update(entry ProjectEntry) (ProjectEntry, error) {
	if entry.Root == "" {
		return ProjectEntry{}, fmt.Errorf("project root cannot be empty")
	}

	entries, err := r.Projects()
	if err != nil {
		return ProjectEntry{}, err
	}
	index := indexOfRoot(entries, entry.Root)
	if index < 0 {
		return ProjectEntry{}, ErrProjectNotFound
	}

	// A caller that supplies no worktree map is updating the other fields;
	// carry the recorded worktrees forward rather than dropping them.
	if entry.Worktrees == nil {
		entry.Worktrees = entries[index].Worktrees
	}

	entries[index] = entry
	if err = r.Set([]string{keyProjects}, entries); err != nil {
		return ProjectEntry{}, fmt.Errorf("project: updating project %q: %w", entry.Root, err)
	}
	if err = r.Write(); err != nil {
		return ProjectEntry{}, fmt.Errorf("project: updating project %q: %w", entry.Root, err)
	}

	return entry, nil
}
