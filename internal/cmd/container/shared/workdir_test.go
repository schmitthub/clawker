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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				path, err := proj.CreateWorktree(context.Background(), "feature/existing", "")
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
				path, err := proj.CreateWorktree(context.Background(), "feature/stale", "")
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

			mgr, err := project.NewProjectManager(cfg, logger.Nop(), gitFactory)
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

			containerOpts := &ContainerOptions{Worktree: tt.branch}
			pmFunc := func() (project.ProjectManager, error) { return mgr, nil }

			wd, projectRootDir, err := resolveWorkDir(
				ctx, containerOpts, cfg,
				"dev", pmFunc, logger.Nop(),
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
		CreateWorktreeFunc: func(_ context.Context, _, _ string) (string, error) {
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

	containerOpts := &ContainerOptions{Worktree: "feature/broken"}
	pmFunc := func() (project.ProjectManager, error) { return mockMgr, nil }
	cfg := configmocks.NewBlankConfig()

	_, _, err := resolveWorkDir(
		context.Background(), containerOpts, cfg,
		"dev", pmFunc, logger.Nop(),
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
				CreateWorktreeFunc: func(_ context.Context, _, _ string) (string, error) {
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

			containerOpts := &ContainerOptions{Worktree: "feature/test"}
			pmFunc := func() (project.ProjectManager, error) { return mockMgr, nil }
			cfg := configmocks.NewBlankConfig()

			_, _, err := resolveWorkDir(
				context.Background(), containerOpts, cfg,
				"dev", pmFunc, logger.Nop(),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
			assert.Contains(t, err.Error(), string(tt.status))
		})
	}
}
