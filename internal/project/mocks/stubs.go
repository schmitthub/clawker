package mocks

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/project"
)

// NewMockProjectManager returns a ProjectManagerMock with safe no-op defaults.
// All methods return zero values instead of panicking via moq's nil-func guard.
// Tests override only the methods they care about.
func NewMockProjectManager() *ProjectManagerMock {
	return &ProjectManagerMock{
		RegisterFunc: func(ctx context.Context, name string, repoPath string) (project.Project, error) {
			return nil, nil
		},
		UpdateFunc: func(ctx context.Context, entry config.ProjectEntry) (project.Project, error) {
			return nil, nil
		},
		ListFunc: func(ctx context.Context) ([]config.ProjectEntry, error) {
			return []config.ProjectEntry{}, nil
		},
		RemoveFunc: func(ctx context.Context, root string) error {
			return nil
		},
		GetFunc: func(ctx context.Context, root string) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
		ResolvePathFunc: func(ctx context.Context, cwd string) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
		CurrentProjectFunc: func(ctx context.Context) (project.Project, error) {
			return nil, project.ErrProjectNotFound
		},
		ListWorktreesFunc: func(ctx context.Context) ([]project.WorktreeState, error) {
			return []project.WorktreeState{}, nil
		},
	}
}

// NewMockProject returns a ProjectMock with the given name and repoPath wired.
// Read accessors (Name, RepoPath, Record) are populated; mutation methods
// (CreateWorktree, RemoveWorktree, etc.) return zero values.
func NewMockProject(name, repoPath string) *ProjectMock {
	return &ProjectMock{
		NameFunc:     func() string { return name },
		RepoPathFunc: func() string { return repoPath },
		RecordFunc: func() (project.ProjectRecord, error) {
			return project.ProjectRecord{
				Name:      name,
				Root:      repoPath,
				Worktrees: map[string]project.WorktreeRecord{},
			}, nil
		},
		CreateWorktreeFunc: func(ctx context.Context, branch, base string) (string, error) {
			return "", nil
		},
		AddWorktreeFunc: func(ctx context.Context, branch, base string) (project.WorktreeState, error) {
			return project.WorktreeState{}, nil
		},
		RemoveWorktreeFunc: func(ctx context.Context, branch string, deleteBranch bool) error {
			return nil
		},
		PruneStaleWorktreesFunc: func(ctx context.Context, dryRun bool) (*project.PruneStaleResult, error) {
			return &project.PruneStaleResult{}, nil
		},
		ListWorktreesFunc: func(ctx context.Context) ([]project.WorktreeState, error) {
			return []project.WorktreeState{}, nil
		},
		GetWorktreeFunc: func(ctx context.Context, branch string) (project.WorktreeState, error) {
			return project.WorktreeState{}, project.ErrWorktreeNotFound
		},
	}
}

// NewTestProjectManager creates a real ProjectManager backed by a file-isolated
// config via NewIsolatedTestConfig. Use this for tests that need actual registry
// persistence (Register, Remove, List round-trips).
// Pass a GitManagerFactory to enable worktree operations, or nil for registry-only tests.
func NewTestProjectManager(t *testing.T, gitFactory project.GitManagerFactory) project.ProjectManager {
	t.Helper()
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	return project.NewProjectManager(cfg, gitFactory)
}
