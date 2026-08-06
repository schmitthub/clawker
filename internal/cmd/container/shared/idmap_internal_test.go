package shared

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// idmapHostConfig builds a spec carrying one workspace bind (moby mount) and
// one user -v bind (string form) under root, plus one bind outside it.
//
//nolint:exhaustruct // moby wire types: only the fields the ID-map path reads matter
func idmapHostConfig(root string) *container.HostConfig {
	return &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: root, Target: "/work"},
			{Type: mount.TypeBind, Source: "/elsewhere", Target: "/other"},
		},
		Binds: []string{root + "/home/.openclaw:/home/clawker/.openclaw"},
	}
}

// TestEnsureIDMappedWorkspace_RootfulDaemonUntouched: the whole mechanism
// exists for rootless daemons. A rootful daemon hands the container the
// host's own IDs, so touching its mounts would be pure risk.
func TestEnsureIDMappedWorkspace_RootfulDaemonUntouched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	hostConfig := idmapHostConfig(root)

	err := ensureIDMappedWorkspace(context.Background(), fake.Client, hostConfig,
		[]string{root}, testIOStreams(), logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, root, hostConfig.Mounts[0].Source)
	assert.Equal(t, root+"/home/.openclaw:/home/clawker/.openclaw", hostConfig.Binds[0])
}

// TestEnsureIDMappedWorkspace_NoBindsUnderRootSkipsDaemon: a snapshot
// workspace binds nothing under the root, so the daemon is never even asked
// — the fake would fail the Info call if it were.
func TestEnsureIDMappedWorkspace_NoBindsUnderRootSkipsDaemon(t *testing.T) {
	t.Parallel()

	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupInfoError(errors.New("the daemon must not be asked"))

	//nolint:exhaustruct // moby wire types: only the fields the ID-map path reads matter
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "clawker-workspace", Target: "/work"}},
	}

	err := ensureIDMappedWorkspace(context.Background(), fake.Client, hostConfig,
		[]string{t.TempDir()}, testIOStreams(), logger.Nop())
	require.NoError(t, err)
	fake.AssertNotCalled(t, "Info")
}

// TestEnsureIDMappedWorkspace_NonInteractiveHandsOverTheCommand: elevation
// needs a human. A headless run must still be actionable, so the error names
// the exact command that unblocks it rather than reporting a bare failure.
func TestEnsureIDMappedWorkspace_NonInteractiveHandsOverTheCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	t.Parallel()

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupRootlessDaemon()

	// testIOStreams is non-TTY, so CanPrompt is false.
	err := ensureIDMappedWorkspace(context.Background(), fake.Client,
		idmapHostConfig(root), []string{root}, testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sudo "+idmapHelperName)
	assert.Contains(t, err.Error(), root)
}

// TestEnsureIDMappedWorkspace_DaemonQueryFailureSurfaces: an unanswerable
// daemon is not a reason to guess. Silently proceeding would create a
// container whose workspace the agent cannot use.
func TestEnsureIDMappedWorkspace_DaemonQueryFailureSurfaces(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	t.Parallel()

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupInfoError(errors.New("daemon unreachable"))

	err := ensureIDMappedWorkspace(context.Background(), fake.Client,
		idmapHostConfig(root), []string{root}, testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon unreachable")
}

// TestWorkspaceRoots_DeepestFirst: a worktree living inside its repository
// must claim its own paths before the repository root sees them, or the
// repository view would swallow the worktree's binds.
func TestWorkspaceRoots_DeepestFirst(t *testing.T) {
	t.Parallel()

	repo := "/home/dev/proj"
	worktree := repo + "/.worktrees/feature"

	//nolint:exhaustruct // moby wire types: only the fields the ID-map path reads matter
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: worktree, Target: "/work"},
			{Type: mount.TypeBind, Source: repo + "/.git", Target: "/git"},
		},
	}

	roots := workspaceRoots(hostConfig, []string{repo, worktree, "", repo})
	require.Len(t, roots, 2)
	assert.Equal(t, worktree, roots[0], "the deeper root must be applied first")
	assert.Equal(t, repo, roots[1])
}

// TestWorkspaceRoots_DropsRootsNothingBinds keeps a candidate that this
// container never mounts from provoking a sudo prompt.
func TestWorkspaceRoots_DropsRootsNothingBinds(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct // moby wire types: only the fields the ID-map path reads matter
	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{{Type: mount.TypeBind, Source: "/home/dev/proj", Target: "/work"}},
	}

	assert.Empty(t, workspaceRoots(hostConfig, []string{"/somewhere/else"}))
}
