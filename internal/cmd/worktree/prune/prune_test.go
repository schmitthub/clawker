package prune

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIOStreams creates an IOStreams instance for testing with captured buffers.
func testIOStreams() (*iostreams.IOStreams, *bytes.Buffer, *bytes.Buffer) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	ios := &iostreams.IOStreams{
		In:     &bytes.Buffer{},
		Out:    outBuf,
		ErrOut: errBuf,
	}
	return ios, outBuf, errBuf
}

func TestPruneRun_NotInProject(t *testing.T) {
	ios, _, _ := testIOStreams()

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	err := pruneRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in a registered project")
}

func TestPruneRun_NoWorktrees(t *testing.T) {
	ios, outBuf, _ := testIOStreams()

	// Create temp dir for registry
	tempDir := t.TempDir()

	// Create registry with a project but no worktrees
	loader := config.NewRegistryLoaderWithPath(tempDir)
	registry := &config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "test-project",
				Root: tempDir,
			},
		},
	}
	err := loader.Save(registry)
	require.NoError(t, err)

	proj := &config.Project{
		Project: "test-project",
	}

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = loader
			return cfg
		},
	}

	err = pruneRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "No worktrees registered")
}

func TestPruneRun_NoStaleEntries(t *testing.T) {
	ios, outBuf, _ := testIOStreams()

	// Create temp dir structure
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	clawkerHome := filepath.Join(tempDir, "clawker")

	// Create project root and git metadata directory
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".git", "worktrees", "feature-branch"), 0755))

	// Create CLAWKER_HOME structure with worktree directory
	clawkerWorktreeDir := filepath.Join(clawkerHome, "projects", "test-project", "worktrees", "feature-branch")
	require.NoError(t, os.MkdirAll(clawkerWorktreeDir, 0755))

	// Create .git file pointing to worktree metadata
	gitContent := "gitdir: " + filepath.Join(projectRoot, ".git", "worktrees", "feature-branch")
	require.NoError(t, os.WriteFile(filepath.Join(clawkerWorktreeDir, ".git"), []byte(gitContent), 0644))

	// Create registry
	loader := config.NewRegistryLoaderWithPath(clawkerHome)
	registry := &config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "test-project",
				Root: projectRoot,
				Worktrees: map[string]string{
					"feature-branch": "feature-branch",
				},
			},
		},
	}
	err := loader.Save(registry)
	require.NoError(t, err)

	proj := &config.Project{
		Project: "test-project",
	}

	// Set CLAWKER_HOME for the test
	t.Setenv("CLAWKER_HOME", clawkerHome)

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = loader
			return cfg
		},
	}

	err = pruneRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "No stale entries")
}

func TestPruneRun_StaleEntry_DryRun(t *testing.T) {
	ios, outBuf, _ := testIOStreams()

	// Create temp dir structure (but don't create worktree dir or git metadata)
	tempDir := t.TempDir()
	clawkerHome := filepath.Join(tempDir, "clawker")
	projectRoot := filepath.Join(tempDir, "project")
	require.NoError(t, os.MkdirAll(clawkerHome, 0755))
	require.NoError(t, os.MkdirAll(projectRoot, 0755))

	// Create registry with a stale worktree entry (no directory, no git metadata)
	loader := config.NewRegistryLoaderWithPath(clawkerHome)
	registry := &config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "test-project",
				Root: projectRoot,
				Worktrees: map[string]string{
					"stale-branch": "stale-branch",
				},
			},
		},
	}
	err := loader.Save(registry)
	require.NoError(t, err)

	proj := &config.Project{
		Project: "test-project",
	}

	// Set CLAWKER_HOME for the test
	t.Setenv("CLAWKER_HOME", clawkerHome)

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = loader
			return cfg
		},
		DryRun: true,
	}

	err = pruneRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "Would remove: stale-branch")
	assert.Contains(t, outBuf.String(), "would be removed")

	// Verify entry still exists (dry run)
	reg, err := loader.Load()
	require.NoError(t, err)
	entry, ok := reg.Projects["test-project"]
	require.True(t, ok)
	assert.Contains(t, entry.Worktrees, "stale-branch")
}

func TestPruneRun_StaleEntry_ActualPrune(t *testing.T) {
	ios, outBuf, _ := testIOStreams()

	// Create temp dir structure (but don't create worktree dir or git metadata)
	tempDir := t.TempDir()
	clawkerHome := filepath.Join(tempDir, "clawker")
	projectRoot := filepath.Join(tempDir, "project")
	require.NoError(t, os.MkdirAll(clawkerHome, 0755))
	require.NoError(t, os.MkdirAll(projectRoot, 0755))

	// Create registry with a stale worktree entry
	loader := config.NewRegistryLoaderWithPath(clawkerHome)
	registry := &config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "test-project",
				Root: projectRoot,
				Worktrees: map[string]string{
					"stale-branch": "stale-branch",
				},
			},
		},
	}
	err := loader.Save(registry)
	require.NoError(t, err)

	proj := &config.Project{
		Project: "test-project",
	}

	// Set CLAWKER_HOME for the test
	t.Setenv("CLAWKER_HOME", clawkerHome)

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = loader
			return cfg
		},
		DryRun: false,
	}

	err = pruneRun(context.Background(), opts)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "Removed: stale-branch")
	assert.Contains(t, outBuf.String(), "stale entry removed")

	// Verify entry was actually removed
	reg, err := loader.Load()
	require.NoError(t, err)
	entry, ok := reg.Projects["test-project"]
	require.True(t, ok)
	assert.NotContains(t, entry.Worktrees, "stale-branch")
}

func TestNewCmdPrune(t *testing.T) {
	ios, _, _ := testIOStreams()

	f := &cmdutil.Factory{
		IOStreams: ios,
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdPrune(f, nil)

	assert.Equal(t, "prune", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Verify flags
	dryRunFlag := cmd.Flags().Lookup("dry-run")
	assert.NotNil(t, dryRunFlag)
	assert.Equal(t, "false", dryRunFlag.DefValue)
}

func TestPruneRun_RegistryNil(t *testing.T) {
	ios, _, _ := testIOStreams()

	proj := &config.Project{
		Project: "test-project",
	}

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = nil // Explicitly nil
			return cfg
		},
	}

	err := pruneRun(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry not available")
}

func TestPruneRun_PartialDeleteFailure(t *testing.T) {
	ios, outBuf, errBuf := testIOStreams()

	// Use in-memory registry with multiple stale worktrees
	inMemRegistry := configtest.NewInMemoryRegistry()
	inMemRegistry.Save(&config.ProjectRegistry{
		Projects: map[string]config.ProjectEntry{
			"test-project": {
				Name: "Test ProjectCfg",
				Root: "/fake/project",
				Worktrees: map[string]string{
					"stale-a": "stale-a",
					"stale-b": "stale-b",
					"stale-c": "stale-c",
				},
			},
		},
	})

	// Set all as stale (prunable)
	inMemRegistry.SetWorktreeState("test-project", "stale-a", false, false)
	inMemRegistry.SetWorktreeState("test-project", "stale-b", false, false)
	inMemRegistry.SetWorktreeState("test-project", "stale-c", false, false)

	// Make stale-b fail to delete
	inMemRegistry.SetWorktreeDeleteError("test-project", "stale-b", errors.New("permission denied"))

	proj := &config.Project{
		Project: "test-project",
	}

	opts := &PruneOptions{
		IOStreams: ios,
		Config: func() config.Provider {
			cfg := config.NewConfigForTest(proj, nil)
			cfg.Registry = inMemRegistry
			return cfg
		},
		DryRun: false,
	}

	err := pruneRun(context.Background(), opts)
	// Should return an error because one deletion failed
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 of 3 entries failed")

	// Should still have processed the successful ones
	output := outBuf.String()
	assert.Contains(t, output, "Removed:")

	// Should have error output for failed deletion
	errOutput := errBuf.String()
	assert.Contains(t, errOutput, "Failed to remove stale-b")
	assert.Contains(t, errOutput, "permission denied")

	// Summary should reflect actual success count
	assert.Contains(t, output, "2 stale entries removed")
}
