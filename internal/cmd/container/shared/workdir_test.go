package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry constructs a registry over the isolated data dir that
// NewIsolatedTestConfig points the CLAWKER_*_DIR env vars at.
func newTestRegistry(t *testing.T) *project.Registry {
	t.Helper()
	reg, err := project.NewRegistry()
	require.NoError(t, err)
	return reg
}

// TestResolveProjectRoot_RegistryErrors pins the contract that a broken
// registry surfaces as an error — it must never silently degrade to the
// working directory, which would change the container's workspace mount
// source. Only the benign ErrNotInProject degrades to "".
func TestResolveProjectRoot_RegistryErrors(t *testing.T) {
	t.Run("nil registry closure errors", func(t *testing.T) {
		_, err := resolveProjectRoot(nil, logger.Nop())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project registry not available")
	})

	t.Run("registry load failure surfaces", func(t *testing.T) {
		closure := func() (*project.Registry, error) {
			return nil, fmt.Errorf("corrupt registry yaml")
		}
		_, err := resolveProjectRoot(closure, logger.Nop())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "corrupt registry yaml")
	})

	t.Run("non-benign resolution failure surfaces", func(t *testing.T) {
		// A zero-value Registry has no store; CurrentRoot fails with a real
		// (non-ErrNotInProject) error that must propagate, not degrade to "".
		closure := func() (*project.Registry, error) {
			return &project.Registry{}, nil
		}
		_, err := resolveProjectRoot(closure, logger.Nop())
		require.Error(t, err)
		assert.NotErrorIs(t, err, project.ErrNotInProject)
	})

	t.Run("not-in-project degrades to empty root", func(t *testing.T) {
		testenv.New(t) // isolated data dir, no registry on disk
		closure := func() (*project.Registry, error) {
			return project.NewRegistry()
		}
		root, err := resolveProjectRoot(closure, logger.Nop())
		require.NoError(t, err)
		assert.Empty(t, root)
	})
}

func TestResolveWorkDir_Worktree(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		setup       func(t *testing.T, proj project.Project) string // returns expected path (empty = don't check)
		wantErr     bool
		errContains string
		checkPath   func(t *testing.T, wd, wantPath string)
	}{
		{
			name:   "creates new worktree",
			branch: "feature/new",
			checkPath: func(t *testing.T, wd, _ string) {
				t.Helper()
				_, err := os.Stat(wd)
				require.NoError(t, err, "worktree directory should exist")
			},
		},
		{
			name:   "reuses existing healthy worktree",
			branch: "feature/existing",
			setup: func(t *testing.T, proj project.Project) string {
				t.Helper()
				path, err := proj.CreateWorktree(context.Background(), "feature/existing", "", false)
				require.NoError(t, err)
				return path
			},
			checkPath: func(t *testing.T, wd, wantPath string) {
				t.Helper()
				_, err := os.Stat(wd)
				require.NoError(t, err, "reused worktree directory should exist")
				assert.Equal(t, wantPath, wd, "reused worktree should return same path")
			},
		},
		{
			name:   "errors on stale worktree with missing directory",
			branch: "feature/stale",
			setup: func(t *testing.T, proj project.Project) string {
				t.Helper()
				path, err := proj.CreateWorktree(context.Background(), "feature/stale", "", false)
				require.NoError(t, err)
				require.NoError(t, os.RemoveAll(path))
				return ""
			},
			wantErr:     true,
			errContains: "clawker worktree prune",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := configmocks.NewIsolatedTestConfig(t)
			root := os.Getenv(cfg.TestRepoDirEnvVar())
			resolvedRoot, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)

			inMemGit := gittest.NewInMemoryGitManager(t, resolvedRoot)
			gitFactory := func(_ string) (*git.GitManager, error) {
				return inMemGit.GitManager, nil
			}

			mgr, err := project.NewProjectManager(logger.Nop(), gitFactory, cfg.Project().Name, newTestRegistry(t))
			require.NoError(t, err)
			ctx := context.Background()

			proj, err := mgr.Register(ctx, "test-app", resolvedRoot)
			require.NoError(t, err)

			oldWd, err := os.Getwd()
			require.NoError(t, err)
			require.NoError(t, os.Chdir(resolvedRoot))
			t.Cleanup(func() { _ = os.Chdir(oldWd) })

			var wantPath string
			if tt.setup != nil {
				wantPath = tt.setup(t, proj)
			}

			containerOpts := &ContainerCreateOptions{Worktree: tt.branch}
			pmFunc := func() (project.ProjectManager, error) { return mgr, nil }

			wd, projectRootDir, err := resolveWorkDir(
				ctx, containerOpts,
				"dev", "", pmFunc, logger.Nop(),
			)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, wd)
			assert.Equal(t, proj.RepoPath(), projectRootDir)
			if tt.checkPath != nil {
				tt.checkPath(t, wd, wantPath)
			}
		})
	}
}

func TestResolveWorkDir_WorktreeGetError(t *testing.T) {
	mockProj := &projectmocks.ProjectMock{
		CreateWorktreeFunc: func(_ context.Context, _, _ string, _ bool) (string, error) {
			return "", project.ErrWorktreeExists
		},
		GetWorktreeFunc: func(_ context.Context, _ string) (project.WorktreeState, error) {
			return project.WorktreeState{}, fmt.Errorf("registry corrupted")
		},
		RepoPathFunc: func() string { return "/fake/root" },
	}
	mockMgr := &projectmocks.ProjectManagerMock{
		CurrentProjectFunc: func(_ context.Context) (project.Project, error) {
			return mockProj, nil
		},
	}

	containerOpts := &ContainerCreateOptions{Worktree: "feature/broken"}
	pmFunc := func() (project.ProjectManager, error) { return mockMgr, nil }

	_, _, err := resolveWorkDir(
		context.Background(), containerOpts,
		"dev", "", pmFunc, logger.Nop(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be retrieved")
}

func TestResolveWorkDir_UnhealthyStatuses(t *testing.T) {
	tests := []struct {
		name        string
		status      project.WorktreeStatus
		errContains string
	}{
		{
			name:        "dotgit_missing status",
			status:      project.WorktreeDotGitMissing,
			errContains: "clawker worktree prune",
		},
		{
			name:        "git_metadata_missing status",
			status:      project.WorktreeGitMetadataMissing,
			errContains: "clawker worktree prune",
		},
		{
			name:        "broken status",
			status:      project.WorktreeBroken,
			errContains: "clawker worktree prune",
		},
		{
			name:        "registry_only status",
			status:      project.WorktreeRegistryOnly,
			errContains: "clawker worktree prune",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProj := &projectmocks.ProjectMock{
				CreateWorktreeFunc: func(_ context.Context, _, _ string, _ bool) (string, error) {
					return "", project.ErrWorktreeExists
				},
				GetWorktreeFunc: func(_ context.Context, branch string) (project.WorktreeState, error) {
					return project.WorktreeState{
						Branch: branch,
						Path:   "/fake/worktree",
						Status: tt.status,
					}, nil
				},
				RepoPathFunc: func() string { return "/fake/root" },
			}
			mockMgr := &projectmocks.ProjectManagerMock{
				CurrentProjectFunc: func(_ context.Context) (project.Project, error) {
					return mockProj, nil
				},
			}

			containerOpts := &ContainerCreateOptions{Worktree: "feature/test"}
			pmFunc := func() (project.ProjectManager, error) { return mockMgr, nil }

			_, _, err := resolveWorkDir(
				context.Background(), containerOpts,
				"dev", "", pmFunc, logger.Nop(),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
			assert.Contains(t, err.Error(), string(tt.status))
		})
	}
}
