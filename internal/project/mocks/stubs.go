package mocks

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/schmitthub/clawker/internal/project"
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
		RegisterFunc: func(_ context.Context, name string, repoPath string) (project.Project, error) {
			return NewProjectMockFromRecord(project.ProjectRecord{
				Name:      name,
				Root:      repoPath,
				Worktrees: map[string]project.WorktreeRecord{},
			}), nil
		},
		UpdateFunc: func(_ context.Context, entry config.ProjectEntry) (project.Project, error) {
			return NewProjectMockFromRecord(projectRecordFromEntry(entry)), nil
		},
		ListFunc: func(_ context.Context) ([]config.ProjectEntry, error) {
			return []config.ProjectEntry{}, nil
		},
		RemoveFunc: func(_ context.Context, _ string) error {
			return nil
		},
		GetFunc: func(_ context.Context, _ string) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
		ResolvePathFunc: func(_ context.Context, _ string) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
		CurrentProjectFunc: func(_ context.Context) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
	}
}

// NewReadOnlyTestManager returns a manager harness coupled to in-memory config and git.
//
// Config uses config.NewFromString (read-only behavior). Register/Update/Remove are disabled.
func NewReadOnlyTestManager(t *testing.T, cfgYAML string) *TestManagerHarness {
	t.Helper()

	cfg := configmocks.NewFromString(cfgYAML)
	realManager := project.NewProjectManager(cfg)

	mgr := NewProjectManagerMock()
	mgr.RegisterFunc = func(_ context.Context, _ string, _ string) (project.Project, error) {
		return nil, ErrReadOnlyTestManager
	}
	mgr.UpdateFunc = func(_ context.Context, _ config.ProjectEntry) (project.Project, error) {
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

	cfg, read := configmocks.NewIsolatedTestConfig(t)
	realManager := project.NewProjectManager(cfg)

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
	return NewProjectMockFromRecord(project.ProjectRecord{
		Name:      "test-project",
		Root:      testRepoRoot,
		Worktrees: map[string]project.WorktreeRecord{},
	})
}

// NewProjectMockFromRecord returns a project mock seeded from the provided record.
func NewProjectMockFromRecord(record project.ProjectRecord) *ProjectMock {
	if record.Worktrees == nil {
		record.Worktrees = map[string]project.WorktreeRecord{}
	}

	return &ProjectMock{
		NameFunc: func() string {
			return record.Name
		},
		RepoPathFunc: func() string {
			return record.Root
		},
		RecordFunc: func() (project.ProjectRecord, error) {
			copied := project.ProjectRecord{
				Name:      record.Name,
				Root:      record.Root,
				Worktrees: make(map[string]project.WorktreeRecord, len(record.Worktrees)),
			}
			for branch, wt := range record.Worktrees {
				copied.Worktrees[branch] = wt
			}
			return copied, nil
		},
		CreateWorktreeFunc: func(_ context.Context, branch, _ string) (string, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return "", project.ErrWorktreeNotFound
			}
			return wt.Path, nil
		},
		AddWorktreeFunc: func(_ context.Context, branch, _ string) (project.WorktreeState, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return project.WorktreeState{}, project.ErrWorktreeNotFound
			}
			return project.WorktreeState{
				Branch:           branch,
				Path:             wt.Path,
				ExistsInRegistry: true,
				ExistsInGit:      true,
				Status:           project.WorktreeHealthy,
			}, nil
		},
		RemoveWorktreeFunc: func(_ context.Context, branch string) error {
			if _, ok := record.Worktrees[branch]; !ok {
				return project.ErrWorktreeNotFound
			}
			return nil
		},
		PruneStaleWorktreesFunc: func(_ context.Context, _ bool) (*project.PruneStaleResult, error) {
			return &project.PruneStaleResult{Failed: map[string]error{}}, nil
		},
		ListWorktreesFunc: func(_ context.Context) ([]project.WorktreeState, error) {
			states := make([]project.WorktreeState, 0, len(record.Worktrees))
			for branch, wt := range record.Worktrees {
				states = append(states, project.WorktreeState{
					Branch:           branch,
					Path:             wt.Path,
					ExistsInRegistry: true,
					ExistsInGit:      true,
					Status:           project.WorktreeHealthy,
				})
			}
			return states, nil
		},
		GetWorktreeFunc: func(_ context.Context, branch string) (project.WorktreeState, error) {
			wt, ok := record.Worktrees[branch]
			if !ok {
				return project.WorktreeState{}, project.ErrWorktreeNotFound
			}
			return project.WorktreeState{
				Branch:           branch,
				Path:             wt.Path,
				ExistsInRegistry: true,
				ExistsInGit:      true,
				Status:           project.WorktreeHealthy,
			}, nil
		},
	}
}

var testRepoRoot = filepath.Join(os.TempDir(), "clawker-test-repo")

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

func delegatingProjectManagerMock(manager project.ProjectManager) *ProjectManagerMock {
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

func projectRecordFromEntry(entry config.ProjectEntry) project.ProjectRecord {
	record := project.ProjectRecord{
		Name:      entry.Name,
		Root:      entry.Root,
		Worktrees: map[string]project.WorktreeRecord{},
	}

	for branch, wt := range entry.Worktrees {
		record.Worktrees[branch] = project.WorktreeRecord{
			Path:   wt.Path,
			Branch: wt.Branch,
		}
	}

	return record
}
