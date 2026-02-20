package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
)

var ErrProjectNotFound = errors.New("project not found")
var ErrProjectExists = errors.New("project already exists")
var ErrWorktreeNotFound = errors.New("worktree not found")
var ErrProjectHandleNotInitialized = errors.New("project handle not initialized")

// ProjectManager provides the only external project-domain API:
// registration/list/get/remove and path-based project resolution.
type ProjectManager interface {
	Register(ctx context.Context, name string, repoPath string) (Project, error)
	Update(ctx context.Context, entry config.ProjectEntry) (Project, error)
	List(ctx context.Context) ([]config.ProjectEntry, error)
	Remove(ctx context.Context, root string) error
	Get(ctx context.Context, root string) (Project, error)
	ResolvePath(ctx context.Context, cwd string) (Project, error)
	CurrentProject(ctx context.Context) (Project, error)
}

// ProjectRecord is the persisted model for a registered project.
type ProjectRecord struct {
	Name      string
	Root      string
	Worktrees map[string]WorktreeRecord
}

// WorktreeRecord is the persisted model for a project worktree.
type WorktreeRecord struct {
	Path   string
	Branch string
}

// WorktreeStatus captures the health/drift status of a worktree.
type WorktreeStatus string

const (
	WorktreeHealthy      WorktreeStatus = "healthy"
	WorktreeRegistryOnly WorktreeStatus = "registry_only"
	WorktreeGitOnly      WorktreeStatus = "git_only"
	WorktreeBroken       WorktreeStatus = "broken"
)

// WorktreeState is a caller-facing merged worktree view.
type WorktreeState struct {
	Branch           string
	Path             string
	Head             string
	IsDetached       bool
	ExistsInRegistry bool
	ExistsInGit      bool
	Status           WorktreeStatus
	InspectError     error
}

// Project is the runtime behavior contract for a single registered project.
// The concrete implementation is package-private.
type Project interface {
	Name() string
	RepoPath() string
	Record() (ProjectRecord, error)
	CreateWorktree(ctx context.Context, branch, base string) (string, error)
	AddWorktree(ctx context.Context, branch, base string) (WorktreeState, error)
	RemoveWorktree(ctx context.Context, branch string) error
	PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error)
	ListWorktrees(ctx context.Context) ([]WorktreeState, error)
	GetWorktree(ctx context.Context, branch string) (WorktreeState, error)
}

type projectHandle struct {
	manager *projectManager
	record  ProjectRecord
}

type projectManager struct {
	cfg    config.Config
	logger iostreams.Logger
}

func NewProjectManager(cfg config.Config, logger iostreams.Logger) ProjectManager {
	return &projectManager{cfg: cfg, logger: logger}
}

// Register adds or updates a project registration and returns a project object.
func (s *projectManager) Register(_ context.Context, name string, repoPath string) (Project, error) {
	entry, err := s.registry().Register(name, repoPath)
	if err != nil {
		return nil, err
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
}

// Update updates an existing registered project by root identity.
func (s *projectManager) Update(_ context.Context, entry config.ProjectEntry) (Project, error) {
	updated, err := s.registry().Update(entry)
	if err != nil {
		return nil, err
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(updated)}, nil
}

// List returns all registered projects.

func (s *projectManager) List(_ context.Context) ([]config.ProjectEntry, error) {
	projects := s.registry().List()
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Root == projects[j].Root {
			return projects[i].Name < projects[j].Name
		}
		return projects[i].Root < projects[j].Root
	})

	result := make([]config.ProjectEntry, len(projects))
	copy(result, projects)
	return result, nil
}

// Remove deletes a project registration.

func (s *projectManager) Remove(_ context.Context, root string) error {
	registry := s.registry()
	if err := registry.RemoveByRoot(root); err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return ErrProjectNotFound
		}
		return err
	}
	if err := registry.Save(); err != nil {
		return err
	}
	return nil
}

// Get loads a registered project by root path.
func (s *projectManager) Get(_ context.Context, root string) (Project, error) {
	entry, ok, err := s.registry().ProjectByRoot(root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrProjectNotFound
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
}

// ResolvePath resolves an arbitrary path to a registered project.
func (s *projectManager) ResolvePath(_ context.Context, cwd string) (Project, error) {
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute path: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath = filepath.Clean(absPath)
	}

	registry := s.registry()
	for _, entry := range registry.List() {
		resolvedRoot, rootErr := filepath.EvalSymlinks(entry.Root)
		if rootErr != nil {
			resolvedRoot = filepath.Clean(entry.Root)
		}
		if resolvedRoot == resolvedPath {
			return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
		}
	}

	return nil, ErrProjectNotFound
}

// CurrentProject resolves the current working directory to a registered project.
func (s *projectManager) CurrentProject(ctx context.Context) (Project, error) {
	if s == nil || s.cfg == nil {
		return nil, fmt.Errorf("project manager not initialized")
	}

	projectRoot, err := s.cfg.GetProjectRoot()
	if err == nil {
		projectFromRoot, resolveErr := s.ResolvePath(ctx, projectRoot)
		if resolveErr == nil {
			return projectFromRoot, nil
		}
	}

	cwd, wdErr := os.Getwd()
	if wdErr != nil {
		return nil, fmt.Errorf("reading current working directory: %w", wdErr)
	}
	return s.ResolvePath(ctx, cwd)
}

// Name returns the project display name.
func (p *projectHandle) Name() string {
	return p.record.Name
}

// RepoPath returns the project repository root path.
func (p *projectHandle) RepoPath() string {
	return p.record.Root
}

// Record returns this project's persisted record.
func (p *projectHandle) Record() (ProjectRecord, error) {
	if p == nil {
		return ProjectRecord{}, ErrProjectHandleNotInitialized
	}
	return p.record, nil
}

// CreateWorktree creates a worktree for this project.
func (p *projectHandle) CreateWorktree(ctx context.Context, branch, base string) (string, error) {
	if p == nil || p.manager == nil {
		return "", ErrProjectHandleNotInitialized
	}
	if err := p.ensureProjectDir(); err != nil {
		return "", err
	}
	worktreePath, err := p.manager.worktrees().CreateWorktree(ctx, branch, base)
	if err != nil {
		return "", err
	}
	if p.record.Worktrees == nil {
		p.record.Worktrees = map[string]WorktreeRecord{}
	}
	p.record.Worktrees[branch] = WorktreeRecord{Path: worktreePath, Branch: branch}
	return worktreePath, nil
}

// AddWorktree creates or reuses a worktree and returns its state.
func (p *projectHandle) AddWorktree(ctx context.Context, branch, base string) (WorktreeState, error) {
	worktreePath, err := p.CreateWorktree(ctx, branch, base)
	if err != nil {
		return WorktreeState{}, err
	}
	return WorktreeState{
		Branch:           branch,
		Path:             worktreePath,
		ExistsInRegistry: true,
		ExistsInGit:      true,
		Status:           WorktreeHealthy,
	}, nil
}

// RemoveWorktree removes a worktree by branch.
func (p *projectHandle) RemoveWorktree(ctx context.Context, branch string) error {
	if p == nil || p.manager == nil {
		return ErrProjectHandleNotInitialized
	}
	if err := p.manager.worktrees().RemoveWorktree(ctx, branch); err != nil {
		return err
	}
	delete(p.record.Worktrees, branch)
	return nil
}

// PruneStaleWorktrees removes stale worktree entries for this project.
func (p *projectHandle) PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error) {
	if p == nil || p.manager == nil {
		return nil, ErrProjectHandleNotInitialized
	}
	result, err := p.manager.worktrees().PruneStaleWorktrees(ctx, dryRun)
	if err != nil {
		return nil, err
	}
	if !dryRun {
		for _, removed := range result.Removed {
			delete(p.record.Worktrees, removed)
		}
	}
	return result, nil
}

// ListWorktrees returns merged worktree state views.
func (p *projectHandle) ListWorktrees(_ context.Context) ([]WorktreeState, error) {
	if p == nil {
		return nil, ErrProjectHandleNotInitialized
	}
	if len(p.record.Worktrees) == 0 {
		return []WorktreeState{}, nil
	}

	states := make([]WorktreeState, 0, len(p.record.Worktrees))
	for branch, wt := range p.record.Worktrees {
		state := WorktreeState{
			Branch:           branch,
			Path:             wt.Path,
			ExistsInRegistry: true,
			ExistsInGit:      true,
			Status:           WorktreeHealthy,
		}
		states = append(states, state)
	}

	return states, nil
}

// GetWorktree returns one worktree state by branch.
func (p *projectHandle) GetWorktree(ctx context.Context, branch string) (WorktreeState, error) {
	if p == nil {
		return WorktreeState{}, ErrProjectHandleNotInitialized
	}
	states, err := p.ListWorktrees(ctx)
	if err != nil {
		return WorktreeState{}, err
	}
	for _, state := range states {
		if state.Branch == branch {
			return state, nil
		}
	}
	return WorktreeState{}, ErrWorktreeNotFound
}

func (s *projectManager) registry() *projectRegistry {
	return newRegistry(s.cfg)
}

func (s *projectManager) worktrees() *worktreeService {
	return newWorktreeService(s.cfg, s.logger)
}

func (p *projectHandle) ensureProjectDir() error {
	if p == nil {
		return ErrProjectHandleNotInitialized
	}
	projectDir := filepath.Join(config.ConfigDir(), "projects")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return fmt.Errorf("ensuring project directory %s: %w", projectDir, err)
	}
	return nil
}

func projectRecordFromEntry(entry config.ProjectEntry) ProjectRecord {
	record := ProjectRecord{
		Name:      entry.Name,
		Root:      entry.Root,
		Worktrees: map[string]WorktreeRecord{},
	}

	for branch, wt := range entry.Worktrees {
		record.Worktrees[branch] = WorktreeRecord{Path: wt.Path, Branch: wt.Branch}
	}

	return record
}
