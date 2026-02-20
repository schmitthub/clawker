package project

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
)

// projectRegistry is an internal facade for project registration and registry operations.
// It is backed by config.Config key ownership and write routing.
type projectRegistry struct {
	cfg config.Config
}

// newRegistry creates a project registry facade backed by the provided config.
func newRegistry(cfg config.Config) *projectRegistry {
	return &projectRegistry{cfg: cfg}
}

// Projects returns all project entries. It supports both legacy map format and new list format.
func (r *projectRegistry) Projects() []config.ProjectEntry {
	if r == nil || r.cfg == nil {
		return []config.ProjectEntry{}
	}

	v, err := r.cfg.Get("projects")
	if err != nil {
		return []config.ProjectEntry{}
	}

	switch raw := v.(type) {
	case []any:
		return decodeProjectList(raw)
	case map[string]any:
		return decodeLegacyProjectMap(raw)
	default:
		return []config.ProjectEntry{}
	}
}

// List returns all project entries in undefined order.
func (r *projectRegistry) List() []config.ProjectEntry {
	entries := r.Projects()
	result := make([]config.ProjectEntry, len(entries))
	copy(result, entries)
	return result
}

func (r *projectRegistry) findByResolvedRoot(root string) (int, config.ProjectEntry, bool, error) {
	if r == nil || r.cfg == nil {
		return -1, config.ProjectEntry{}, false, fmt.Errorf("registry not initialized")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return -1, config.ProjectEntry{}, false, fmt.Errorf("failed to get absolute path: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = absRoot
	}

	entries := r.Projects()
	for i, entry := range entries {
		entryResolvedRoot, evalErr := filepath.EvalSymlinks(entry.Root)
		if evalErr != nil {
			entryResolvedRoot = entry.Root
		}
		if entryResolvedRoot == resolvedRoot {
			return i, entry, true, nil
		}
	}

	return -1, config.ProjectEntry{}, false, nil
}

func (r *projectRegistry) ProjectByRoot(root string) (config.ProjectEntry, bool, error) {
	_, entry, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return config.ProjectEntry{}, false, err
	}
	return entry, ok, nil
}

func (r *projectRegistry) setProjects(entries []config.ProjectEntry) error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("registry not initialized")
	}

	raw := make([]any, 0, len(entries))
	for _, entry := range entries {
		entryAny := map[string]any{
			"name": entry.Name,
			"root": entry.Root,
		}
		if len(entry.Worktrees) > 0 {
			worktrees := make(map[string]any, len(entry.Worktrees))
			for wtName, wt := range entry.Worktrees {
				wtAny := map[string]any{}
				if wt.Path != "" {
					wtAny["path"] = wt.Path
				}
				if wt.Branch != "" {
					wtAny["branch"] = wt.Branch
				}
				worktrees[wtName] = wtAny
			}
			entryAny["worktrees"] = worktrees
		}
		raw = append(raw, entryAny)
	}

	return r.cfg.Set("projects", raw)
}

func (r *projectRegistry) RemoveByRoot(root string) error {
	index, _, ok, err := r.findByResolvedRoot(root)
	if err != nil {
		return err
	}
	if !ok {
		return ErrProjectNotFound
	}

	entries := r.Projects()
	entries = append(entries[:index], entries[index+1:]...)
	return r.setProjects(entries)
}

func (r *projectRegistry) registerWorktree(projectRoot, branch, path string) error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("registry not initialized")
	}
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	index, entry, ok, err := r.findByResolvedRoot(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}
	if entry.Worktrees == nil {
		entry.Worktrees = map[string]config.WorktreeEntry{}
	}
	entry.Worktrees[branch] = config.WorktreeEntry{Path: path, Branch: branch}

	entries := r.Projects()
	entries[index] = entry
	return r.setProjects(entries)
}

func (r *projectRegistry) unregisterWorktree(projectRoot, branch string) error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("registry not initialized")
	}
	if projectRoot == "" {
		return fmt.Errorf("project root cannot be empty")
	}
	if branch == "" {
		return fmt.Errorf("worktree branch cannot be empty")
	}

	index, entry, ok, err := r.findByResolvedRoot(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %q not found in registry", projectRoot)
	}
	if len(entry.Worktrees) == 0 {
		return nil
	}

	delete(entry.Worktrees, branch)
	entries := r.Projects()
	entries[index] = entry
	return r.setProjects(entries)
}

// Save persists staged project registry changes to projects.yaml.
func (r *projectRegistry) Save() error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("registry not initialized")
	}
	err := r.cfg.Write(config.WriteOptions{Scope: config.ScopeRegistry})
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "project registry path is not configured") {
		registryPath := filepath.Join(config.ConfigDir(), r.cfg.ProjectRegistryFileName())
		return r.cfg.Write(config.WriteOptions{Scope: config.ScopeRegistry, Path: registryPath})
	}
	return err
}

// Register adds a project by root path.
func (r *projectRegistry) Register(displayName, rootDir string) (config.ProjectEntry, error) {
	if r == nil || r.cfg == nil {
		return config.ProjectEntry{}, fmt.Errorf("registry not initialized")
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return config.ProjectEntry{}, fmt.Errorf("failed to get absolute path: %w", err)
	}

	if _, _, ok, err := r.findByResolvedRoot(absRoot); err != nil {
		return config.ProjectEntry{}, err
	} else if ok {
		return config.ProjectEntry{}, ErrProjectExists
	}

	entry := config.ProjectEntry{Name: displayName, Root: absRoot}
	entries := r.Projects()
	entries = append(entries, entry)
	if err := r.setProjects(entries); err != nil {
		return config.ProjectEntry{}, err
	}
	if err := r.Save(); err != nil {
		return config.ProjectEntry{}, err
	}

	return entry, nil
}

func (r *projectRegistry) Update(entry config.ProjectEntry) (config.ProjectEntry, error) {
	if r == nil || r.cfg == nil {
		return config.ProjectEntry{}, fmt.Errorf("registry not initialized")
	}
	if entry.Root == "" {
		return config.ProjectEntry{}, fmt.Errorf("project root cannot be empty")
	}

	index, existing, ok, err := r.findByResolvedRoot(entry.Root)
	if err != nil {
		return config.ProjectEntry{}, err
	}
	if !ok {
		return config.ProjectEntry{}, ErrProjectNotFound
	}

	if entry.Worktrees == nil {
		entry.Worktrees = existing.Worktrees
	}

	entries := r.Projects()
	entries[index] = entry
	if err := r.setProjects(entries); err != nil {
		return config.ProjectEntry{}, err
	}
	if err := r.Save(); err != nil {
		return config.ProjectEntry{}, err
	}

	return entry, nil
}

func decodeProjectList(raw []any) []config.ProjectEntry {
	decoded := make([]config.ProjectEntry, 0, len(raw))
	for _, rawEntry := range raw {
		entryMap, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		decoded = append(decoded, decodeProjectEntry(entryMap))
	}
	return decoded
}

func decodeLegacyProjectMap(raw map[string]any) []config.ProjectEntry {
	decoded := make([]config.ProjectEntry, 0, len(raw))
	for _, rawEntry := range raw {
		entryMap, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		decoded = append(decoded, decodeProjectEntry(entryMap))
	}
	return decoded
}

func decodeProjectEntry(entryMap map[string]any) config.ProjectEntry {
	entry := config.ProjectEntry{}
	if name, ok := entryMap["name"].(string); ok {
		entry.Name = name
	}
	if root, ok := entryMap["root"].(string); ok {
		entry.Root = root
	}

	if rawWorktrees, ok := entryMap["worktrees"].(map[string]any); ok {
		entry.Worktrees = make(map[string]config.WorktreeEntry, len(rawWorktrees))
		for wtName, rawWt := range rawWorktrees {
			switch wt := rawWt.(type) {
			case map[string]any:
				worktree := config.WorktreeEntry{}
				if path, ok := wt["path"].(string); ok {
					worktree.Path = path
				}
				if branch, ok := wt["branch"].(string); ok {
					worktree.Branch = branch
				}
				entry.Worktrees[wtName] = worktree
			case string:
				entry.Worktrees[wtName] = config.WorktreeEntry{Path: wt}
			}
		}
	}

	return entry
}
