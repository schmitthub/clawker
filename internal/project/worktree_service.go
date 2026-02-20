package project

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

var ErrNotInProjectPath = errors.New("not in a registered project path")
var ErrProjectNotRegistered = errors.New("project root is not registered")

type gitManager interface {
	SetupWorktree(dirs git.WorktreeDirProvider, branch, base string) (string, error)
	RemoveWorktree(dirs git.WorktreeDirProvider, branch string) error
	Worktrees() (*git.WorktreeManager, error)
}

type gitManagerFactory func(projectRoot string) (gitManager, error)

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
	logger          iostreams.Logger
	newManager      gitManagerFactory
	registryFactory func() worktreeRegistry
}

func newWorktreeService(cfg config.Config, logger iostreams.Logger) *worktreeService {
	return &worktreeService{
		cfg:    cfg,
		logger: logger,
		newManager: func(projectRoot string) (gitManager, error) {
			return git.NewGitManager(projectRoot)
		},
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
		s.logError(err, "project root resolution failed")
		if errors.Is(err, config.ErrNotInProject) {
			return "", ErrNotInProjectPath
		}
		return "", fmt.Errorf("resolving project root: %w", err)
	}

	_, err = s.findProjectByRoot(projectRoot)
	if err != nil {
		s.logError(err, "project registry lookup failed")
		return "", err
	}

	manager, err := s.newManager(projectRoot)
	if err != nil {
		s.logError(err, "git manager initialization failed")
		if errors.Is(err, git.ErrNotRepository) {
			return "", fmt.Errorf("project root is not a git repository: %w", err)
		}
		return "", fmt.Errorf("initializing git manager: %w", err)
	}

	provider := &configDirWorktreeProvider{baseDir: projectWorktreeBaseDir(s.cfg, projectRoot)}
	worktreePath, err := manager.SetupWorktree(provider, branch, base)
	if err != nil {
		s.logError(err, "git worktree setup failed")
		return "", fmt.Errorf("creating worktree: %w", err)
	}

	registry := s.registryFactory()
	if err := registry.registerWorktree(projectRoot, branch, worktreePath); err != nil {
		s.logError(err, "registry staging failed after worktree create")
		_ = manager.RemoveWorktree(provider, branch)
		return "", fmt.Errorf("updating project registry: %w", err)
	}

	if err := registry.Save(); err != nil {
		s.logError(err, "registry save failed after worktree create")
		_ = manager.RemoveWorktree(provider, branch)
		return "", fmt.Errorf("saving project registry: %w", err)
	}

	return worktreePath, nil
}

func (s *worktreeService) RemoveWorktree(_ context.Context, branch string) error {
	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		s.logError(err, "project root resolution failed")
		if errors.Is(err, config.ErrNotInProject) {
			return ErrNotInProjectPath
		}
		return fmt.Errorf("resolving project root: %w", err)
	}

	_, err = s.findProjectByRoot(projectRoot)
	if err != nil {
		s.logError(err, "project registry lookup failed")
		return err
	}

	manager, err := s.newManager(projectRoot)
	if err != nil {
		s.logError(err, "git manager initialization failed")
		if errors.Is(err, git.ErrNotRepository) {
			return fmt.Errorf("project root is not a git repository: %w", err)
		}
		return fmt.Errorf("initializing git manager: %w", err)
	}

	provider := &configDirWorktreeProvider{baseDir: projectWorktreeBaseDir(s.cfg, projectRoot)}
	if err := manager.RemoveWorktree(provider, branch); err != nil {
		s.logError(err, "git worktree remove failed")
		return fmt.Errorf("removing worktree: %w", err)
	}

	registry := s.registryFactory()
	if err := registry.unregisterWorktree(projectRoot, branch); err != nil {
		s.logError(err, "registry update failed after worktree remove")
		return fmt.Errorf("updating project registry: %w", err)
	}
	if err := registry.Save(); err != nil {
		s.logError(err, "registry save failed after worktree remove")
		return fmt.Errorf("saving project registry: %w", err)
	}

	return nil
}

func (s *worktreeService) CurrentProject() (config.ProjectEntry, error) {
	projectRoot, err := s.cfg.GetProjectRoot()
	if err != nil {
		s.logError(err, "project root resolution failed")
		if errors.Is(err, config.ErrNotInProject) {
			return config.ProjectEntry{}, ErrNotInProjectPath
		}
		return config.ProjectEntry{}, fmt.Errorf("resolving project root: %w", err)
	}

	projectEntry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		s.logError(err, "project registry lookup failed")
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
		s.logError(err, "project root resolution failed")
		if errors.Is(err, config.ErrNotInProject) {
			return nil, ErrNotInProjectPath
		}
		return nil, fmt.Errorf("resolving project root: %w", err)
	}

	manager, err := s.newManager(projectRoot)
	if err != nil {
		s.logError(err, "git manager initialization failed")
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("project root is not a git repository: %w", err)
		}
		return nil, fmt.Errorf("initializing git manager: %w", err)
	}

	wtMgr, err := manager.Worktrees()
	if err != nil {
		s.logError(err, "worktree manager initialization failed")
		return nil, fmt.Errorf("initializing worktree manager: %w", err)
	}

	for name, wt := range projectEntry.Worktrees {
		slug := text.Slugify(name)
		path := wt.Path
		if path == "" {
			path = filepath.Join(projectWorktreeBaseDir(s.cfg, projectRoot), slug)
		}

		_, statErr := os.Stat(path)
		dirMissing := statErr != nil && os.IsNotExist(statErr)
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("checking worktree %s: %w", name, statErr)
		}

		gitExists, err := wtMgr.Exists(slug)
		if err != nil {
			return nil, fmt.Errorf("checking git worktree %s: %w", name, err)
		}

		if dirMissing && !gitExists {
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

func projectWorktreeBaseDir(cfg config.Config, projectRoot string) string {
	worktreesRoot, err := cfg.WorktreesSubdir()
	if err != nil || worktreesRoot == "" {
		worktreesRoot = filepath.Join(config.ConfigDir(), "worktrees")
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		absRoot = projectRoot
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = filepath.Clean(absRoot)
	}
	sum := sha1.Sum([]byte(resolvedRoot))
	return filepath.Join(worktreesRoot, fmt.Sprintf("%x", sum[:6]))
}

// NewWorktreeDirProvider creates a WorktreeDirProvider rooted at the
// standard worktree namespace for the given project root.
// This is the exported constructor for external callers (e.g. container/shared)
// that need a WorktreeDirProvider without going through the full project service.
func NewWorktreeDirProvider(cfg config.Config, projectRoot string) git.WorktreeDirProvider {
	return &configDirWorktreeProvider{
		baseDir: projectWorktreeBaseDir(cfg, projectRoot),
	}
}

func (s *worktreeService) logError(err error, msg string) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Error().Err(err).Msg(msg)
}

type configDirWorktreeProvider struct {
	baseDir string
}

func (p *configDirWorktreeProvider) GetOrCreateWorktreeDir(name string) (string, error) {
	path := filepath.Join(p.baseDir, text.Slugify(name))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("creating worktree directory: %w", err)
	}
	return path, nil
}

func (p *configDirWorktreeProvider) GetWorktreeDir(name string) (string, error) {
	path := filepath.Join(p.baseDir, text.Slugify(name))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("worktree directory not found: %s", path)
		}
		return "", fmt.Errorf("checking worktree directory: %w", err)
	}
	return path, nil
}

func (p *configDirWorktreeProvider) DeleteWorktreeDir(name string) error {
	path := filepath.Join(p.baseDir, text.Slugify(name))
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
