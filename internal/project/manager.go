package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/logger"
)

var ErrProjectNotFound = errors.New("project not found")
var ErrProjectExists = errors.New("project already exists")
var ErrWorktreeNotFound = errors.New("worktree not found")
var ErrProjectHandleNotInitialized = errors.New("project handle not initialized")

// ProjectStatus captures the health of a project's root directory.
type ProjectStatus string

const (
	ProjectOK           ProjectStatus = "ok"
	ProjectMissing      ProjectStatus = "missing"
	ProjectInaccessible ProjectStatus = "inaccessible"
)

// ProjectState is a caller-facing enriched project view.
// Analogous to WorktreeState: combines registry data with runtime checks.
type ProjectState struct {
	Name      string
	Root      string
	Worktrees []WorktreeState
	Status    ProjectStatus
	StatusErr error // non-nil when Status is ProjectInaccessible
}

// ProjectManager provides the only external project-domain API:
// registration/list/get/remove and path-based project resolution.

//go:generate moq -rm -pkg mocks -out mocks/manager_mock.go . ProjectManager
type ProjectManager interface {
	Register(ctx context.Context, name string, repoPath string) (Project, error)
	Update(ctx context.Context, entry ProjectEntry) (Project, error)
	List(ctx context.Context) ([]ProjectEntry, error)
	ListProjects(ctx context.Context) ([]ProjectState, error)
	Remove(ctx context.Context, root string) error
	Get(ctx context.Context, root string) (Project, error)
	ResolvePath(ctx context.Context, cwd string) (Project, error)
	CurrentProject(ctx context.Context) (Project, error)
	ListWorktrees(ctx context.Context) ([]WorktreeState, error)
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
	WorktreeHealthy            WorktreeStatus = "healthy"
	WorktreeRegistryOnly       WorktreeStatus = "registry_only"
	WorktreeGitOnly            WorktreeStatus = "git_only"
	WorktreeBroken             WorktreeStatus = "broken"
	WorktreeDotGitMissing      WorktreeStatus = "dotgit_missing"
	WorktreeGitMetadataMissing WorktreeStatus = "git_metadata_missing"
)

// WorktreeState is a caller-facing merged worktree view.
type WorktreeState struct {
	Project          string
	Branch           string
	Path             string
	Head             string
	IsDetached       bool
	ExistsInRegistry bool
	ExistsInGit      bool
	Status           WorktreeStatus
	IsLocked         bool  // worktree is locked against pruning (.git/worktrees/<slug>/locked exists)
	InspectError     error // non-nil indicates degraded health check (permissions, git errors)
}

// Project is the runtime behavior contract for a single registered project.
// The concrete implementation is package-private.
//
//go:generate moq -rm -pkg mocks -out mocks/project_mock.go . Project
type Project interface {
	Name() string
	RepoPath() string
	Record() (ProjectRecord, error)
	CreateWorktree(ctx context.Context, branch, base string) (string, error)
	AddWorktree(ctx context.Context, branch, base string) (WorktreeState, error)
	RemoveWorktree(ctx context.Context, branch string, deleteBranch bool) error
	PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error)
	ListWorktrees(ctx context.Context) ([]WorktreeState, error)
	GetWorktree(ctx context.Context, branch string) (WorktreeState, error)
}

// GitManagerFactory creates a git.GitManager for the given project root.
// Production callers pass git.NewGitManager; tests pass a factory returning
// gittest.InMemoryGitManager.GitManager.
type GitManagerFactory func(projectRoot string) (*git.GitManager, error)

type projectHandle struct {
	manager *projectManager
	record  ProjectRecord
}

type projectManager struct {
	nameOverride string
	log          *logger.Logger
	reg          *Registry
	newGitMgr    GitManagerFactory
}

// NewProjectManager builds a project manager over an injected Registry — the
// manager never constructs registry storage itself. nameOverride is the
// config-owned project name (clawker.yaml `name:`), resolved by the caller and
// passed as a primitive so this package never imports config. config resolves
// its own walk-up anchor from Registry.CurrentRoot, so the dependency runs one
// way — the manager reads config-derived values, config never reads the
// manager.
func NewProjectManager(log *logger.Logger, gitFactory GitManagerFactory, nameOverride string, reg *Registry) (ProjectManager, error) {
	if reg == nil {
		return nil, fmt.Errorf("project: registry is required")
	}
	if gitFactory == nil {
		gitFactory = func(root string) (*git.GitManager, error) {
			return git.NewGitManager(root)
		}
	}
	return &projectManager{nameOverride: nameOverride, log: log, reg: reg, newGitMgr: gitFactory}, nil
}

// Register adds or updates a project registration and returns a project object.
func (s *projectManager) Register(_ context.Context, name string, repoPath string) (Project, error) {
	entry, err := s.reg.register(name, repoPath)
	if err != nil {
		return nil, err
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
}

// Update updates an existing registered project by root identity.
func (s *projectManager) Update(_ context.Context, entry ProjectEntry) (Project, error) {
	updated, err := s.reg.update(entry)
	if err != nil {
		return nil, err
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(updated)}, nil
}

// List returns all registered projects.

func (s *projectManager) List(_ context.Context) ([]ProjectEntry, error) {
	projects := s.reg.list()
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Root == projects[j].Root {
			return projects[i].Name < projects[j].Name
		}
		return projects[i].Root < projects[j].Root
	})

	result := make([]ProjectEntry, len(projects))
	copy(result, projects)
	return result, nil
}

// ListProjects returns enriched project views with runtime health checks.
func (s *projectManager) ListProjects(ctx context.Context) ([]ProjectState, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	states := make([]ProjectState, 0, len(entries))
	for _, e := range entries {
		state := ProjectState{
			Name: e.Name,
			Root: e.Root,
		}

		// Check root directory health.
		info, statErr := os.Stat(e.Root)
		switch {
		case statErr == nil && info.IsDir():
			state.Status = ProjectOK
		case statErr != nil && errors.Is(statErr, fs.ErrNotExist):
			state.Status = ProjectMissing
		case statErr == nil && !info.IsDir():
			state.Status = ProjectInaccessible
			state.StatusErr = fmt.Errorf("path exists but is not a directory: %s", e.Root)
		default:
			state.Status = ProjectInaccessible
			state.StatusErr = statErr
		}

		// Enrich worktree state if project root is accessible.
		if state.Status == ProjectOK {
			proj, getErr := s.Get(ctx, e.Root)
			if getErr == nil {
				state.Worktrees, _ = proj.ListWorktrees(ctx)
			}
		}

		states = append(states, state)
	}

	return states, nil
}

// Remove deletes a project registration.

func (s *projectManager) Remove(_ context.Context, root string) error {
	if err := s.reg.removeByRoot(root); err != nil {
		if errors.Is(err, ErrProjectNotFound) {
			return ErrProjectNotFound
		}
		return err
	}
	if err := s.reg.save(); err != nil {
		return err
	}
	return nil
}

// Get loads a registered project by root path.
func (s *projectManager) Get(_ context.Context, root string) (Project, error) {
	entry, ok, err := s.reg.projectByRoot(root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrProjectNotFound
	}
	return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
}

// ResolvePath resolves an arbitrary path to a registered project. Both sides
// are normalized via resolveRootPath (absolute + symlink-resolved with a
// cleaned fallback) so symlinked and real paths match interchangeably.
func (s *projectManager) ResolvePath(_ context.Context, cwd string) (Project, error) {
	resolvedPath := resolveRootPath(cwd)

	for _, entry := range s.reg.list() {
		if resolveRootPath(entry.Root) == resolvedPath {
			return &projectHandle{manager: s, record: projectRecordFromEntry(entry)}, nil
		}
	}

	return nil, ErrProjectNotFound
}

// CurrentProject resolves the current working directory to a registered project.
//
// If clawker.yaml::name is set, the returned Project reports that
// override as its Name() while the underlying registry row is otherwise
// untouched. The CLI hierarchy is: env (none) < clawker.yaml::name
// (file) < --name flag (handled at init/register write path, persisting
// the chosen value into the registry).
func (s *projectManager) CurrentProject(ctx context.Context) (Project, error) {
	if s == nil {
		return nil, fmt.Errorf("project manager not initialized")
	}
	var resolved Project
	var resolveErr error
	projectRoot, err := s.reg.CurrentRoot()
	// ErrNotInProject is the benign "no registered project for CWD" condition
	// and degrades to the cwd-based fallback below; any other error is a real
	// registry/storage failure and must surface instead of being mistaken for
	// an unregistered directory.
	if err != nil && !errors.Is(err, ErrNotInProject) {
		return nil, fmt.Errorf("resolving current project root: %w", err)
	}
	if err == nil {
		resolved, resolveErr = s.ResolvePath(ctx, projectRoot)
	}
	if resolved == nil {
		cwd, wdErr := os.Getwd()
		if wdErr != nil {
			return nil, fmt.Errorf("reading current working directory: %w", wdErr)
		}
		resolved, resolveErr = s.ResolvePath(ctx, cwd)
	}
	if resolveErr != nil {
		return nil, resolveErr
	}

	if override := strings.TrimSpace(s.nameOverride); override != "" {
		if h, ok := resolved.(*projectHandle); ok {
			h.record.Name = override
		}
	}
	return resolved, nil
}

// ListWorktrees returns worktree states across all registered projects.
func (s *projectManager) ListWorktrees(ctx context.Context) ([]WorktreeState, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	var all []WorktreeState
	for _, entry := range entries {
		proj, err := s.Get(ctx, entry.Root)
		if err != nil {
			s.log.Debug().Err(err).Str("root", entry.Root).Msg("skipping project in worktree listing")
			continue
		}
		states, err := proj.ListWorktrees(ctx)
		if err != nil {
			s.log.Debug().Err(err).Str("root", entry.Root).Msg("skipping project worktrees due to listing error")
			continue
		}
		all = append(all, states...)
	}
	return all, nil
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
	worktreePath, err := p.manager.worktrees().CreateWorktree(ctx, p.record.Root, branch, base)
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
		Project:          p.record.Name,
		Branch:           branch,
		Path:             worktreePath,
		ExistsInRegistry: true,
		ExistsInGit:      true,
		Status:           WorktreeHealthy,
	}, nil
}

// RemoveWorktree removes a worktree by branch. If deleteBranch is true,
// the branch ref is also deleted via GitManager.DeleteBranch (safe deletion:
// refuses unmerged commits and current branch).
// The worktree is always removed even if branch deletion fails.
func (p *projectHandle) RemoveWorktree(ctx context.Context, branch string, deleteBranch bool) error {
	if p == nil || p.manager == nil {
		return ErrProjectHandleNotInitialized
	}
	err := p.manager.worktrees().RemoveWorktree(ctx, p.record.Root, branch, deleteBranch)
	// Worktree is gone regardless of branch deletion outcome — always update record.
	delete(p.record.Worktrees, branch)
	return err
}

// PruneStaleWorktrees removes stale worktree entries for this project.
func (p *projectHandle) PruneStaleWorktrees(ctx context.Context, dryRun bool) (*PruneStaleResult, error) {
	if p == nil || p.manager == nil {
		return nil, ErrProjectHandleNotInitialized
	}
	result, err := p.manager.worktrees().PruneStaleWorktrees(ctx, p.record.Root, dryRun)
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

// ListWorktrees returns merged worktree state views with actual health checks.
// It uses the git layer to inspect each worktree for HEAD, branch, and detached
// state, then combines that with registry, disk, and git metadata state to set Status.
//
// Health checks performed per worktree:
//   - Directory exists on disk
//   - .git file present inside worktree directory (file, not directory)
//   - Git metadata exists in parent repo (.git/worktrees/<slug>/)
//   - Branch ref exists in git
//   - Lock file present (.git/worktrees/<slug>/locked)
//
// Filesystem errors other than "not found" are propagated via InspectError
// rather than silently treated as "missing".
func (p *projectHandle) ListWorktrees(_ context.Context) ([]WorktreeState, error) {
	if p == nil {
		return nil, ErrProjectHandleNotInitialized
	}
	if len(p.record.Worktrees) == 0 {
		return []WorktreeState{}, nil
	}

	// Try to get git manager for detailed worktree inspection.
	var gitMgr *git.GitManager
	var gitMgrErr error
	if p.manager != nil && p.manager.newGitMgr != nil {
		mgr, err := p.manager.newGitMgr(p.record.Root)
		if err != nil {
			gitMgrErr = err
		} else {
			gitMgr = mgr
		}
	}

	// Build git.WorktreeDirEntry slice from registry for the git layer
	var entries []git.WorktreeDirEntry
	for branch, wt := range p.record.Worktrees {
		if wt.Path == "" {
			continue
		}
		entries = append(entries, git.WorktreeDirEntry{
			Name: branch,
			Slug: filepath.Base(wt.Path),
			Path: wt.Path,
		})
	}

	// Get git-level info (HEAD, branch, detached state) for each worktree
	gitInfoByBranch := make(map[string]git.WorktreeInfo)
	if gitMgr != nil && len(entries) > 0 {
		infos, err := gitMgr.ListWorktrees(entries)
		if err == nil {
			for _, info := range infos {
				gitInfoByBranch[info.Name] = info
			}
		}
	}

	// Get worktree manager for metadata existence and lock checks
	var wtMgr *git.WorktreeManager
	if gitMgr != nil {
		wm, err := gitMgr.Worktrees()
		if err == nil {
			wtMgr = wm
		}
	}

	states := make([]WorktreeState, 0, len(p.record.Worktrees))
	for branch, wt := range p.record.Worktrees {
		state := WorktreeState{
			Project:          p.record.Name,
			Branch:           branch,
			Path:             wt.Path,
			ExistsInRegistry: true,
		}

		// Check if directory exists on disk
		_, statErr := os.Stat(wt.Path)
		dirExists := statErr == nil
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			state.InspectError = fmt.Errorf("checking worktree directory: %w", statErr)
		}

		// Check .git file inside worktree dir (must be a file, not directory for linked worktrees)
		dotGitOK := false
		if dirExists {
			dotGitPath := filepath.Join(wt.Path, ".git")
			info, err := os.Stat(dotGitPath)
			if err == nil {
				dotGitOK = !info.IsDir()
			} else if !errors.Is(err, fs.ErrNotExist) {
				if state.InspectError == nil {
					state.InspectError = fmt.Errorf("checking .git in worktree: %w", err)
				}
			}
		}

		// Check git metadata existence (.git/worktrees/<slug>/)
		slug := filepath.Base(wt.Path)
		gitMetadataExists := false
		if wtMgr != nil {
			exists, err := wtMgr.Exists(slug)
			if err == nil {
				gitMetadataExists = exists
			}
		}

		// Check lock file (.git/worktrees/<slug>/locked)
		if gitMgr != nil {
			locked, lockErr := gitMgr.IsWorktreeLocked(slug)
			if lockErr != nil {
				if state.InspectError == nil {
					state.InspectError = fmt.Errorf("checking lock: %w", lockErr)
				}
			} else {
				state.IsLocked = locked
			}
		}

		// Check if branch exists in git
		var branchExists bool
		var branchCheckErr error
		if gitMgr != nil {
			exists, err := gitMgr.BranchExists(branch)
			if err != nil {
				branchCheckErr = err
			} else {
				branchExists = exists
			}
		} else if gitMgrErr != nil {
			branchCheckErr = gitMgrErr
		}

		state.ExistsInGit = branchExists

		// Merge git-level detail from ListWorktrees
		if info, ok := gitInfoByBranch[branch]; ok {
			if !info.Head.IsZero() {
				state.Head = info.Head.String()[:7]
			}
			state.IsDetached = info.IsDetached
			if info.Error != nil {
				state.InspectError = info.Error
			}
		}

		// Determine health status with expanded checks
		switch {
		case !dirExists:
			state.Status = WorktreeRegistryOnly
		case dirExists && !dotGitOK:
			state.Status = WorktreeDotGitMissing
		case dirExists && wtMgr != nil && !gitMetadataExists:
			state.Status = WorktreeGitMetadataMissing
		case dirExists && !branchExists && branchCheckErr == nil:
			state.Status = WorktreeBroken
		case dirExists && branchCheckErr != nil:
			// Can't verify branch — degrade gracefully with InspectError
			state.Status = WorktreeHealthy
			if state.InspectError == nil {
				state.InspectError = branchCheckErr
			}
		default:
			state.Status = WorktreeHealthy
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

func (s *projectManager) worktrees() *worktreeService {
	return newWorktreeService(s.log, s.reg, s.newGitMgr)
}

func projectRecordFromEntry(entry ProjectEntry) ProjectRecord {
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
