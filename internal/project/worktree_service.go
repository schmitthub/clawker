package project

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/text"
)

var (
	ErrNotInProjectPath = errors.New("not in a registered project path")
	ErrWorktreeExists   = errors.New("worktree already exists for branch")
)

type PruneStaleResult struct {
	Prunable []string
	Removed  []string
	Failed   map[string]error
	Locked   []string // worktrees skipped due to git worktree lock
}

type worktreeService struct {
	log       *logger.Logger
	reg       Registry
	newGitMgr GitManagerFactory
}

func newWorktreeService(log *logger.Logger, reg Registry, gitFactory GitManagerFactory) *worktreeService {
	return &worktreeService{
		log:       log,
		reg:       reg,
		newGitMgr: gitFactory,
	}
}

func (s *worktreeService) CreateWorktree(
	_ context.Context,
	projectRoot, branch, base string,
	noTrack bool,
) (string, error) {
	return s.addWorktree(projectRoot, branch, base, noTrack)
}

func (s *worktreeService) addWorktree(projectRoot, branch, base string, noTrack bool) (string, error) {
	entry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return "", err
	}

	// Reject duplicate worktree
	if _, exists := entry.Worktrees[branch]; exists {
		return "", fmt.Errorf("branch %q: %w", branch, ErrWorktreeExists)
	}

	manager, err := s.gitManager(projectRoot)
	if err != nil {
		return "", err
	}

	provider, err := newFlatWorktreeDirProvider(projectRoot, entry)
	if err != nil {
		return "", err
	}
	worktreePath, err := manager.SetupWorktree(provider, branch, base, noTrack)
	if err != nil {
		return "", fmt.Errorf("creating worktree: %w", err)
	}

	if regErr := s.reg.RegisterWorktree(projectRoot, branch, worktreePath); regErr != nil {
		_ = manager.RemoveWorktree(provider, branch)
		return "", fmt.Errorf("updating project registry: %w", regErr)
	}

	return worktreePath, nil
}

func (s *worktreeService) RemoveWorktree(_ context.Context, projectRoot, branch string, deleteBranch bool) error {
	entry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return err
	}

	manager, err := s.gitManager(projectRoot)
	if err != nil {
		return err
	}

	provider, err := newFlatWorktreeDirProvider(projectRoot, entry)
	if err != nil {
		return err
	}
	if err := manager.RemoveWorktree(provider, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}

	if regErr := s.reg.UnregisterWorktree(projectRoot, branch); regErr != nil {
		return fmt.Errorf("updating project registry: %w", regErr)
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

func (s *worktreeService) PruneStaleWorktrees(
	_ context.Context,
	projectRoot string,
	dryRun bool,
) (*PruneStaleResult, error) {
	projectEntry, err := s.findProjectByRoot(projectRoot)
	if err != nil {
		return nil, err
	}

	result := &PruneStaleResult{Failed: make(map[string]error)}
	if len(projectEntry.Worktrees) == 0 {
		return result, nil
	}

	manager, err := s.gitManager(projectRoot)
	if err != nil {
		return nil, err
	}

	wtMgr, err := manager.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("initializing worktree manager: %w", err)
	}

	for name, wt := range projectEntry.Worktrees {
		verdict, classifyErr := classifyStaleWorktree(manager, wtMgr, name, wt.Path)
		if classifyErr != nil {
			return nil, classifyErr
		}
		switch verdict {
		case worktreePrunable:
			result.Prunable = append(result.Prunable, name)
		case worktreeLocked:
			result.Locked = append(result.Locked, name)
		case worktreeLive:
		}
	}

	if dryRun {
		return result, nil
	}
	s.unregisterPrunable(projectRoot, result)
	return result, nil
}

// gitManager opens the project root's git manager, translating the
// not-a-repository case into the caller-facing message.
func (s *worktreeService) gitManager(projectRoot string) (*git.GitManager, error) {
	manager, err := s.newGitMgr(projectRoot)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("project root is not a git repository: %w", err)
		}
		return nil, fmt.Errorf("initializing git manager: %w", err)
	}
	return manager, nil
}

// unregisterPrunable drops each prunable worktree from the registry, recording
// per-entry failures in result.Failed rather than aborting the sweep.
func (s *worktreeService) unregisterPrunable(projectRoot string, result *PruneStaleResult) {
	for _, name := range result.Prunable {
		if err := s.reg.UnregisterWorktree(projectRoot, name); err != nil {
			result.Failed[name] = fmt.Errorf("updating project registry: %w", err)
			continue
		}
		result.Removed = append(result.Removed, name)
	}
}

// pruneVerdict is the per-worktree outcome of the staleness check.
type pruneVerdict int

const (
	// worktreeLive: the worktree is intact and stays registered.
	worktreeLive pruneVerdict = iota
	// worktreePrunable: the worktree drifted (directory, git metadata, or
	// branch gone) and its registry entry can be dropped.
	worktreePrunable
	// worktreeLocked: the worktree drifted but is locked against pruning.
	worktreeLocked
)

// classifyStaleWorktree decides whether one registered worktree is prunable.
// A worktree is stale when its directory, its git metadata, or its branch is
// gone; a stale worktree that git has locked (`git worktree lock`) is reported
// as locked instead so pruning skips it. Filesystem and git errors other than
// "not found" abort the whole prune rather than being read as staleness.
func classifyStaleWorktree(
	manager *git.GitManager,
	wtMgr *git.WorktreeManager,
	name, path string,
) (pruneVerdict, error) {
	if path == "" {
		// Path must be set — an entry with no path can only be dropped.
		return worktreePrunable, nil
	}

	_, statErr := os.Stat(path)
	dirExists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return worktreeLive, fmt.Errorf("checking worktree %s: %w", name, statErr)
	}

	// Use directory basename for git worktree existence check — matches how
	// SetupWorktree registers worktrees with git (by dir basename, not branch name).
	wtName := filepath.Base(path)
	gitWorktreeExists, err := wtMgr.Exists(wtName)
	if err != nil {
		return worktreeLive, fmt.Errorf("checking git worktree %s: %w", name, err)
	}

	branchExists, err := manager.BranchExists(name)
	if err != nil {
		return worktreeLive, fmt.Errorf("checking branch %s: %w", name, err)
	}

	if dirExists && gitWorktreeExists && branchExists {
		return worktreeLive, nil
	}

	locked, err := manager.IsWorktreeLocked(wtName)
	if err != nil {
		return worktreeLive, fmt.Errorf("checking worktree lock %s: %w", name, err)
	}
	if locked {
		return worktreeLocked, nil
	}
	return worktreePrunable, nil
}

func (s *worktreeService) findProjectByRoot(projectRoot string) (ProjectEntry, error) {
	if projectRoot == "" {
		return ProjectEntry{}, errors.New(
			"project root cannot be empty; the project registry may be corrupted — try re-registering with 'clawker project register'",
		)
	}
	entries, err := s.reg.Projects()
	if err != nil {
		return ProjectEntry{}, fmt.Errorf("listing projects: %w", err)
	}
	index := indexOfRoot(entries, projectRoot)
	if index < 0 {
		return ProjectEntry{}, ErrProjectNotRegistered
	}
	return entries[index], nil
}

// generateWorktreeDirName produces a flat directory name for a worktree:
// <repoName>-<projectName>-<sha256(uuid)[:12]>
func generateWorktreeDirName(repoName, projectName string) string {
	id := uuid.New()
	sum := sha256.Sum256(id[:])
	return fmt.Sprintf("%s-%s-%x",
		text.Slugify(repoName),
		text.Slugify(projectName),
		sum[:6],
	)
}

// newFlatWorktreeDirProvider creates a flatWorktreeDirProvider from a project
// entry. It ensures the worktrees root directory exists; a failure to create
// it is a real error and is propagated.
func newFlatWorktreeDirProvider(projectRoot string, entry ProjectEntry) (*flatWorktreeDirProvider, error) {
	worktreesRoot, err := consts.WorktreesSubdir()
	if err != nil {
		return nil, fmt.Errorf("ensuring worktrees directory: %w", err)
	}

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
		worktreesRoot: worktreesRoot,
		repoName:      repoName,
		projectName:   projectName,
		knownPaths:    knownPaths,
	}, nil
}

// flatWorktreeDirProvider implements git.WorktreeDirProvider with flat UUID-based naming.
// Worktree directories are placed directly under the worktrees root:
//
//	<WorktreesRoot>/<repoName>-<projectName>-<sha256(uuid)[:12]>
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
