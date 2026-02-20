package project

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/git/gittest"
)

var ErrReadOnlyTestManager = errors.New("test manager is read-only")

type TestManagerHarness struct {
	Manager         *ProjectManagerMock
	Config          config.Config
	Git             *gittest.InMemoryGitManager
	ReadConfigFiles func(settingsOut io.Writer, projectOut io.Writer, registryOut io.Writer)
}

// NewProjectManagerMock returns a panic-safe mock manager with default behavior.
func NewProjectManagerMock() *ProjectManagerMock {
	return &ProjectManagerMock{
		RegisterFunc: func(_ context.Context, name string, repoPath string) (Project, error) {
			return NewProjectMockFromRecord(ProjectRecord{
				Name:      name,
				Root:      repoPath,
				Worktrees: map[string]WorktreeRecord{},
			}), nil
		},
		UpdateFunc: func(_ context.Context, entry config.ProjectEntry) (Project, error) {
			return NewProjectMockFromRecord(projectRecordFromEntry(entry)), nil
		},
		ListFunc: func(_ context.Context) ([]config.ProjectEntry, error) {
			return []config.ProjectEntry{}, nil
		},
		RemoveFunc: func(_ context.Context, _ string) error {
			return nil
		},
		GetFunc: func(_ context.Context, _ string) (Project, error) {
			return nil, ErrProjectNotFound
		},
		ResolvePathFunc: func(_ context.Context, _ string) (Project, error) {
			return nil, ErrProjectNotFound
		},
		CurrentProjectFunc: func(_ context.Context) (Project, error) {
			return nil, ErrProjectNotFound
		},
	}
}

// NewReadOnlyTestManager returns a manager harness coupled to in-memory config and git.
//
// Config uses config.NewFromString (read-only behavior). Register/Update/Remove are disabled.
func NewReadOnlyTestManager(t *testing.T, cfgYAML string) *TestManagerHarness {
	t.Helper()

	cfg := config.NewFromString(cfgYAML)
	realManager := NewProjectManager(cfg)

	mgr := NewProjectManagerMock()
	mgr.RegisterFunc = func(_ context.Context, _ string, _ string) (Project, error) {
		return nil, ErrReadOnlyTestManager
	}
	mgr.UpdateFunc = func(_ context.Context, _ config.ProjectEntry) (Project, error) {
		return nil, ErrReadOnlyTestManager
	}
	mgr.RemoveFunc = func(_ context.Context, _ string) error {
		return ErrReadOnlyTestManager
	}
	mgr.ListFunc = realManager.List
	mgr.GetFunc = realManager.Get
	mgr.ResolvePathFunc = realManager.ResolvePath
	mgr.CurrentProjectFunc = realManager.CurrentProject

	gitManager := gittest.NewInMemoryGitManager(t, testRepoRootFromConfig(cfg))

	return &TestManagerHarness{
		Manager: mgr,
		Config:  cfg,
		Git:     gitManager,
	}
}

// NewIsolatedTestManager returns a manager harness coupled to isolated writable config and in-memory git.
//
// Config uses config.NewIsolatedTestConfig, allowing Set/Write in tests.
func NewIsolatedTestManager(t *testing.T) *TestManagerHarness {
	t.Helper()

	cfg, read := config.NewIsolatedTestConfig(t)
	realManager := NewProjectManager(cfg)

	gitManager := gittest.NewInMemoryGitManager(t, testRepoRootFromConfig(cfg))

	return &TestManagerHarness{
		Manager:         delegatingProjectManagerMock(realManager),
		Config:          cfg,
		Git:             gitManager,
		ReadConfigFiles: read,
	}
}

// NewProjectMock returns a panic-safe project mock with default behavior.
func NewProjectMock() *ProjectMock {
	return NewProjectMockFromRecord(ProjectRecord{
		Name:      "test-project",
		Root:      testRepoRoot,
		Worktrees: map[string]WorktreeRecord{},
	})
}

// NewProjectMockFromRecord returns a project mock seeded from the provided record.
func NewProjectMockFromRecord(record ProjectRecord) *ProjectMock {
	if record.Worktrees == nil {
		record.Worktrees = map[string]WorktreeRecord{}
	}

	return &ProjectMock{
		NameFunc: func() string {
			return record.Name
		},
		RepoPathFunc: func() string {
			return record.Root
		},
		RecordFunc: func() (ProjectRecord, error) {
			copied := ProjectRecord{
				Name:      record.Name,
				Root:      record.Root,
				Worktrees: make(map[string]WorktreeRecord, len(record.Worktrees)),
			}
			for branch, wt := range record.Worktrees {
				copied.Worktrees[branch] = wt
			}
			return copied, nil
		},
		CreateWorktreeFunc: func(_ context.Context, branch, _ string) (string, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return "", ErrWorktreeNotFound
			}
			return wt.Path, nil
		},
		AddWorktreeFunc: func(_ context.Context, branch, _ string) (WorktreeState, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return WorktreeState{}, ErrWorktreeNotFound
			}
			return WorktreeState{
				Branch:           branch,
				Path:             wt.Path,
				ExistsInRegistry: true,
				ExistsInGit:      true,
				Status:           WorktreeHealthy,
			}, nil
		},
		RemoveWorktreeFunc: func(_ context.Context, branch string) error {
			if _, ok := record.Worktrees[branch]; !ok {
				return ErrWorktreeNotFound
			}
			return nil
		},
		PruneStaleWorktreesFunc: func(_ context.Context, _ bool) (*PruneStaleResult, error) {
			return &PruneStaleResult{Failed: map[string]error{}}, nil
		},
		ListWorktreesFunc: func(_ context.Context) ([]WorktreeState, error) {
			states := make([]WorktreeState, 0, len(record.Worktrees))
			for branch, wt := range record.Worktrees {
				states = append(states, WorktreeState{
					Branch:           branch,
					Path:             wt.Path,
					ExistsInRegistry: true,
					ExistsInGit:      true,
					Status:           WorktreeHealthy,
				})
			}
			return states, nil
		},
		GetWorktreeFunc: func(_ context.Context, branch string) (WorktreeState, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return WorktreeState{}, ErrWorktreeNotFound
			}
			return WorktreeState{
				Branch:           branch,
				Path:             wt.Path,
				ExistsInRegistry: true,
				ExistsInGit:      true,
				Status:           WorktreeHealthy,
			}, nil
		},
	}
}

const testRepoRoot = "/tmp/clawker-test-repo"

func testRepoRootFromConfig(cfg config.Config) string {
	if cfg == nil {
		return testRepoRoot
	}
	projectRoot, err := cfg.GetProjectRoot()
	if err != nil || projectRoot == "" {
		return testRepoRoot
	}
	return projectRoot
}

func delegatingProjectManagerMock(manager ProjectManager) *ProjectManagerMock {
	if manager == nil {
		return NewProjectManagerMock()
	}

	return &ProjectManagerMock{
		RegisterFunc:       manager.Register,
		UpdateFunc:         manager.Update,
		ListFunc:           manager.List,
		RemoveFunc:         manager.Remove,
		GetFunc:            manager.Get,
		ResolvePathFunc:    manager.ResolvePath,
		CurrentProjectFunc: manager.CurrentProject,
	}
}

