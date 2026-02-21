package project

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/text"
)

var ErrNotInProjectPath = errors.New("not in a registered project path")
var ErrProjectNotRegistered = errors.New("project root is not registered")
var ErrWorktreeExists = errors.New("worktree already exists for branch")

type worktreeRegistry interface {
	Projects() []config.ProjectEntry
	registerWorktree(projectRoot, branch, path string) error
	unregisterWorktree(projectRoot, branch string) error
	Save() error
}

type PruneStaleResult struct {
	Prunable []string
	Removed  []string
	Failed   map[string]error
}

type worktreeService struct {
	cfg             config.Config
	newGitMgr       GitManagerFactory
	registryFactory func() worktreeRegistry
}

func newWorktreeService(cfg config.Config, gitFactory GitManagerFactory) *worktreeService {
	return &worktreeService{
		cfg:       cfg,
		newGitMgr: gitFactory,
		registryFactory: func() worktreeRegistry {
			return newRegistry(cfg)
		},
	}
}

func (s *worktreeService) CreateWorktree(ctx context.Context, branch, base string) (string, error) {
	return s.AddWorktree(ctx, branch, base)
}

func (s *worktreeService) AddWorktree(_ context.Context, branch, base string) (string, error) {
	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInProject) {
			return "", ErrNotInProjectPath
		}
		return "", fmt.Errorf("resolving project root: %w", err)
	}

	entry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return "", err
	}

	// Reject duplicate worktree
	if _, exists := entry.Worktrees[branch]; exists {
		return "", fmt.Errorf("branch %q: %w", branch, ErrWorktreeExists)
	}

	manager, err := s.newGitMgr(projectRoot)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return "", fmt.Errorf("project root is not a git repository: %w", err)
		}
		return "", fmt.Errorf("initializing git manager: %w", err)
	}

	provider := newFlatWorktreeDirProvider(s.cfg, projectRoot, entry)
	worktreePath, err := manager.SetupWorktree(provider, branch, base)
	if err != nil {
		return "", fmt.Errorf("creating worktree: %w", err)
	}

	registry := s.registryFactory()
	if err := registry.registerWorktree(projectRoot, branch, worktreePath); err != nil {
		_ = manager.RemoveWorktree(provider, branch)
		return "", fmt.Errorf("updating project registry: %w", err)
	}

	if err := registry.Save(); err != nil {
		_ = manager.RemoveWorktree(provider, branch)
		return "", fmt.Errorf("saving project registry: %w", err)
	}

	return worktreePath, nil
}

func (s *worktreeService) RemoveWorktree(_ context.Context, branch string, deleteBranch bool) error {
	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInProject) {
			return ErrNotInProjectPath
		}
		return fmt.Errorf("resolving project root: %w", err)
	}

	entry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return err
	}

	manager, err := s.newGitMgr(projectRoot)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return fmt.Errorf("project root is not a git repository: %w", err)
		}
		return fmt.Errorf("initializing git manager: %w", err)
	}

	provider := newFlatWorktreeDirProvider(s.cfg, projectRoot, entry)
	if err := manager.RemoveWorktree(provider, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}

	registry := s.registryFactory()
	if err := registry.unregisterWorktree(projectRoot, branch); err != nil {
		return fmt.Errorf("updating project registry: %w", err)
	}
	if err := registry.Save(); err != nil {
		return fmt.Errorf("saving project registry: %w", err)
	}

	if deleteBranch {
		if err := manager.DeleteBranch(branch); err != nil {
			if !errors.Is(err, git.ErrBranchNotFound) {
				return fmt.Errorf("worktree removed but deleting branch: %w", err)
			}
		}
	}

	return nil
}

func (s *worktreeService) CurrentProject() (config.ProjectEntry, error) {
	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInProject) {
			return config.ProjectEntry{}, ErrNotInProjectPath
		}
		return config.ProjectEntry{}, fmt.Errorf("resolving project root: %w", err)
	}

	projectEntry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return config.ProjectEntry{}, err
	}

	return projectEntry, nil
}

func (s *worktreeService) PruneStaleWorktrees(_ context.Context, dryRun bool) (*PruneStaleResult, error) {
	projectEntry, err := s.CurrentProject()
	if err != nil {
		return nil, err
	}

	result := &PruneStaleResult{Failed: make(map[string]error)}
	if len(projectEntry.Worktrees) == 0 {
		return result, nil
	}

	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInProject) {
			return nil, ErrNotInProjectPath
		}
		return nil, fmt.Errorf("resolving project root: %w", err)
	}

	manager, err := s.newGitMgr(projectRoot)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("project root is not a git repository: %w", err)
		}
		return nil, fmt.Errorf("initializing git manager: %w", err)
	}

	wtMgr, err := manager.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("initializing worktree manager: %w", err)
	}

	for name, wt := range projectEntry.Worktrees {
		path := wt.Path
		if path == "" {
			// Path must be set — skip entries with missing paths
			result.Prunable = append(result.Prunable, name)
			continue
		}

		_, statErr := os.Stat(path)
		dirExists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("checking worktree %s: %w", name, statErr)
		}

		// Use directory basename for git worktree existence check — matches how
		// SetupWorktree registers worktrees with git (by dir basename, not branch name).
		wtName := filepath.Base(path)
		gitWorktreeExists, err := wtMgr.Exists(wtName)
		if err != nil {
			return nil, fmt.Errorf("checking git worktree %s: %w", name, err)
		}

		branchExists, err := manager.BranchExists(name)
		if err != nil {
			return nil, fmt.Errorf("checking branch %s: %w", name, err)
		}

		// Prunable if any of: dir missing, git worktree metadata gone, or branch gone
		if !dirExists || !gitWorktreeExists || !branchExists {
			result.Prunable = append(result.Prunable, name)
		}
	}

	if dryRun {
		return result, nil
	}

	registry := s.registryFactory()
	for _, name := range result.Prunable {
		if err := registry.unregisterWorktree(projectRoot, name); err != nil {
			result.Failed[name] = fmt.Errorf("updating project registry: %w", err)
			continue
		}
		if err := registry.Save(); err != nil {
			result.Failed[name] = fmt.Errorf("saving project registry: %w", err)
			continue
		}
		result.Removed = append(result.Removed, name)
	}

	return result, nil
}

func (s *worktreeService) findProjectByRoot(projectRoot string) (config.ProjectEntry, error) {
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		resolvedProjectRoot = filepath.Clean(projectRoot)
	}

	registry := s.registryFactory()
	projects := registry.Projects()
	for _, entry := range projects {
		resolvedEntryRoot, err := filepath.EvalSymlinks(entry.Root)
		if err != nil {
			resolvedEntryRoot = filepath.Clean(entry.Root)
		}
		if resolvedEntryRoot == resolvedProjectRoot {
			return entry, nil
		}
	}

	return config.ProjectEntry{}, ErrProjectNotRegistered
}

// worktreesRootDir returns the base directory where all worktree directories live.
func worktreesRootDir(cfg config.Config) string {
	root, err := cfg.WorktreesSubdir()
	if err != nil || root == "" {
		return filepath.Join(config.ConfigDir(), "worktrees")
	}
	return root
}

// generateWorktreeDirName produces a flat directory name for a worktree:
// <repoName>-<projectName>-<sha1(uuid)[:12]>
func generateWorktreeDirName(repoName, projectName string) string {
	id := uuid.New()
	sum := sha1.Sum(id[:])
	return fmt.Sprintf("%s-%s-%x",
		text.Slugify(repoName),
		text.Slugify(projectName),
		sum[:6],
	)
}

// NewWorktreeDirProvider creates a WorktreeDirProvider for the given project.
// It looks up the project in the registry to populate known worktree paths,
// enabling path reuse for existing worktrees and UUID-based generation for new ones.
// External callers (e.g. container/shared) use this instead of the full project service.
func NewWorktreeDirProvider(cfg config.Config, projectRoot string) git.WorktreeDirProvider {
	registry := newRegistry(cfg)
	projects := registry.Projects()

	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		resolvedRoot = filepath.Clean(projectRoot)
	}

	var entry config.ProjectEntry
	for _, p := range projects {
		resolvedEntry, err := filepath.EvalSymlinks(p.Root)
		if err != nil {
			resolvedEntry = filepath.Clean(p.Root)
		}
		if resolvedEntry == resolvedRoot {
			entry = p
			break
		}
	}

	return newFlatWorktreeDirProvider(cfg, projectRoot, entry)
}

// newFlatWorktreeDirProvider creates a flatWorktreeDirProvider from a project entry.
func newFlatWorktreeDirProvider(cfg config.Config, projectRoot string, entry config.ProjectEntry) *flatWorktreeDirProvider {
	knownPaths := make(map[string]string, len(entry.Worktrees))
	for branch, wt := range entry.Worktrees {
		if wt.Path != "" {
			knownPaths[branch] = wt.Path
		}
	}

	repoName := filepath.Base(projectRoot)
	projectName := entry.Name
	if projectName == "" {
		projectName = repoName
	}

	return &flatWorktreeDirProvider{
		worktreesRoot: worktreesRootDir(cfg),
		repoName:      repoName,
		projectName:   projectName,
		knownPaths:    knownPaths,
	}
}

// flatWorktreeDirProvider implements git.WorktreeDirProvider with flat UUID-based naming.
// Worktree directories are placed directly under the worktrees root:
//
//	<WorktreesRoot>/<repoName>-<projectName>-<sha1(uuid)[:12]>
//
// Known paths from the registry are reused for existing worktrees.
// New worktrees get a freshly generated UUID-based directory name.
type flatWorktreeDirProvider struct {
	worktreesRoot string
	repoName      string
	projectName   string
	knownPaths    map[string]string // branch -> absolute path (from registry)
}

func (p *flatWorktreeDirProvider) GetOrCreateWorktreeDir(name string) (string, error) {
	// Reuse known path for existing worktrees
	if path, ok := p.knownPaths[name]; ok {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("creating worktree directory: %w", err)
		}
		return path, nil
	}

	// Generate new UUID-based directory
	dirName := generateWorktreeDirName(p.repoName, p.projectName)
	path := filepath.Join(p.worktreesRoot, dirName)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("creating worktree directory: %w", err)
	}

	// Track the new path so RemoveWorktree can find it on cleanup
	p.knownPaths[name] = path
	return path, nil
}

func (p *flatWorktreeDirProvider) GetWorktreeDir(name string) (string, error) {
	path, ok := p.knownPaths[name]
	if !ok {
		return "", fmt.Errorf("worktree %q not found in registry", name)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("worktree directory not found: %s", path)
		}
		return "", fmt.Errorf("checking worktree directory: %w", err)
	}
	return path, nil
}

func (p *flatWorktreeDirProvider) DeleteWorktreeDir(name string) error {
	path, ok := p.knownPaths[name]
	if !ok {
		return fmt.Errorf("worktree %q not found in registry", name)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("worktree directory not found: %s", path)
		}
		return fmt.Errorf("checking worktree directory: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("deleting worktree directory: %w", err)
	}
	return nil
}
