package mocks

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
)

// NewMockProjectManager returns a ProjectManagerMock with safe no-op defaults.
// All methods return zero values instead of panicking via moq's nil-func guard.
// Tests override only the methods they care about.
func NewMockProjectManager() *ProjectManagerMock {
	return &ProjectManagerMock{
		RegisterFunc: func(ctx context.Context, name string, repoPath string) (project.Project, error) {
			return nil, nil
		},
		UpdateFunc: func(ctx context.Context, entry project.ProjectEntry) (project.Project, error) {
			return nil, nil
		},
		ListFunc: func(ctx context.Context) ([]project.ProjectEntry, error) {
			return []project.ProjectEntry{}, nil
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
		ListProjectsFunc: func(ctx context.Context) ([]project.ProjectState, error) {
			return []project.ProjectState{}, nil
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
		CreateWorktreeFunc: func(ctx context.Context, branch, base string, noTrack bool) (string, error) {
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
// config via testenv. Use this for tests that need actual registry
// persistence (Register, Remove, List round-trips).
// Pass a GitManagerFactory to enable worktree operations, or nil for registry-only tests.
func NewTestProjectManager(t *testing.T, gitFactory project.GitManagerFactory) project.ProjectManager {
	t.Helper()
	env := testenv.New(t, testenv.WithProjectManager(gitFactory))
	return env.ProjectManager()
}

// ProjectState builds the project.ProjectState value a test hands to code that
// renders or inspects registry state (project list/info, worktree commands).
//
// With no mutators it is the empty-registry-row baseline: no name, no root,
// the zero status, no worktrees, and a nil StatusErr (a healthy row carries
// none). Tests state only the fields they assert on, so the omitted-field
// decision lives here once instead of being respelled as a sparse struct
// literal at every call site:
//
//	st := projectmocks.ProjectState(func(s *project.ProjectState) {
//		s.Name, s.Status = "alpha", project.ProjectMissing
//	})
func ProjectState(mutators ...func(*project.ProjectState)) project.ProjectState {
	var state project.ProjectState
	for _, mutate := range mutators {
		mutate(&state)
	}
	return state
}

// WorktreeState is the ProjectState sibling for project.WorktreeState — the
// same contract, for the worktree rows hanging off a project or returned by
// ListWorktrees/GetWorktree. The bare call is the "nothing inspected yet"
// baseline: no branch or path, absent from both registry and git, the zero
// status, and a nil InspectError (non-nil means a degraded health check).
func WorktreeState(mutators ...func(*project.WorktreeState)) project.WorktreeState {
	var state project.WorktreeState
	for _, mutate := range mutators {
		mutate(&state)
	}
	return state
}
