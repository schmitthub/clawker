package shared

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/git/gittest"
	"github.com/schmitthub/clawker/internal/logger/loggertest"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWorkDir_Worktree(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		setup       func(t *testing.T, proj project.Project)
		wantErr     bool
		errContains string
		checkPath   func(t *testing.T, wd string)
	}{
		{
			name:   "creates new worktree",
			branch: "feature/new",
			checkPath: func(t *testing.T, wd string) {
				t.Helper()
				_, err := os.Stat(wd)
				require.NoError(t, err, "worktree directory should exist")
			},
		},
		{
			name:   "reuses existing healthy worktree",
			branch: "feature/existing",
			setup: func(t *testing.T, proj project.Project) {
				t.Helper()
				_, err := proj.CreateWorktree(context.Background(), "feature/existing", "")
				require.NoError(t, err)
			},
			checkPath: func(t *testing.T, wd string) {
				t.Helper()
				_, err := os.Stat(wd)
				require.NoError(t, err, "reused worktree directory should exist")
			},
		},
		{
			name:   "errors on stale worktree with missing directory",
			branch: "feature/stale",
			setup: func(t *testing.T, proj project.Project) {
				t.Helper()
				path, err := proj.CreateWorktree(context.Background(), "feature/stale", "")
				require.NoError(t, err)
				require.NoError(t, os.RemoveAll(path))
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

			mgr, err := project.NewProjectManager(cfg, gitFactory)
			require.NoError(t, err)
			ctx := context.Background()

			proj, err := mgr.Register(ctx, "test-app", resolvedRoot)
			require.NoError(t, err)

			oldWd, err := os.Getwd()
			require.NoError(t, err)
			require.NoError(t, os.Chdir(resolvedRoot))
			t.Cleanup(func() { _ = os.Chdir(oldWd) })

			if tt.setup != nil {
				tt.setup(t, proj)
			}

			containerOpts := &ContainerOptions{Worktree: tt.branch}
			pmFunc := func() (project.ProjectManager, error) { return mgr, nil }

			wd, projectRootDir, err := resolveWorkDir(
				ctx, containerOpts, cfg,
				"dev", pmFunc, loggertest.NewNop(),
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
				tt.checkPath(t, wd)
			}
		})
	}
}
