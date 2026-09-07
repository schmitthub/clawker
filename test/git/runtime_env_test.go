package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/docker"
)

func TestRuntimeEnvGitOwnership(t *testing.T) {
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	root := t.TempDir()
	baseEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	}
	repo := filepath.Join(root, "shared workspace")
	other := filepath.Join(root, "other")
	for _, dir := range []string{repo, other} {
		cmd := exec.CommandContext(t.Context(), git, "init", dir)
		cmd.Env = baseEnv
		output, initErr := cmd.CombinedOutput()
		require.NoError(t, initErr, "%s", output)
	}
	worktree := filepath.Join(root, "linked worktree")
	for _, args := range [][]string{
		{"-C", repo, "commit", "--allow-empty", "-m", "fixture"},
		{"-C", repo, "worktree", "add", "--detach", worktree},
	} {
		cmd := exec.CommandContext(t.Context(), git, args...)
		cmd.Env = baseEnv
		output, setupErr := cmd.CombinedOutput()
		require.NoError(t, setupErr, "%s", output)
	}

	for _, tc := range []struct {
		name     string
		path     string
		worktree bool
		gpg      bool
	}{
		{name: "bind", path: repo, worktree: false, gpg: false},
		{name: "worktree with GPG", path: worktree, worktree: true, gpg: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts docker.RuntimeEnvOpts
			opts.WorkspaceSource = tc.path
			opts.Worktree = tc.worktree
			opts.GPGForwardingEnabled = tc.gpg
			env, envErr := docker.RuntimeEnv(opts)
			require.NoError(t, envErr)
			// Exercise Git's ownership rejection without root privileges or host chown.
			cmdEnv := slices.Concat(baseEnv, env, []string{"GIT_TEST_ASSUME_DIFFERENT_OWNER=1"})

			cmd := exec.CommandContext(t.Context(), git, "-C", tc.path, "status", "--porcelain")
			cmd.Env = cmdEnv
			output, statusErr := cmd.CombinedOutput()
			require.NoError(t, statusErr, "%s", output)

			cmd = exec.CommandContext(t.Context(), git, "-C", other, "status", "--porcelain")
			cmd.Env = cmdEnv
			output, statusErr = cmd.CombinedOutput()
			require.Error(t, statusErr)
			require.Contains(t, string(output), "dubious ownership")

			if tc.gpg {
				cmd = exec.CommandContext(t.Context(), git, "config", "--get", "gpg.program")
				cmd.Env = cmdEnv
				configOutput, configErr := cmd.CombinedOutput()
				require.NoError(t, configErr, "%s", configOutput)
				require.Equal(t, "/usr/bin/gpg\n", string(configOutput))
			}
		})
	}
}
