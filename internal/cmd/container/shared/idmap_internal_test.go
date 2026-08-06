package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// stubSubIDFiles points the subordinate-ID seam at synthetic contents keyed
// to the current uid, so mapping outcomes do not depend on the host's
// /etc/subuid. Tests that use it cannot run in parallel (package-level seam)
// and skip under root, whose workspaces ComputeMapping refuses by design.
func stubSubIDFiles(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("mapping a root-owned workspace is refused by design")
	}
	prev := readSubIDFiles
	t.Cleanup(func() { readSubIDFiles = prev })
	content := fmt.Sprintf("%d:100000:65536\n", os.Getuid())
	readSubIDFiles = func() (string, string, error) {
		return content, content, nil
	}
}

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
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	t.Parallel()

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	hostConfig := idmapHostConfig(root)

	roots, err := ensureIDMappedWorkspace(context.Background(), fake.Client, hostConfig,
		[]string{root}, testIOStreams(), logger.Nop())
	require.NoError(t, err)
	assert.Empty(t, roots)
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

	roots, err := ensureIDMappedWorkspace(context.Background(), fake.Client, hostConfig,
		[]string{t.TempDir()}, testIOStreams(), logger.Nop())
	require.NoError(t, err)
	assert.Empty(t, roots)
	fake.AssertNotCalled(t, "Info")
}

// TestEnsureIDMappedWorkspace_NonInteractiveDeclines: attaching a view takes
// a sudo prompt, which needs a person at a terminal. There is no command to
// hand a headless operator — the remedy IS an interactive run — so the error
// names the situation and the remedy, not a command.
func TestEnsureIDMappedWorkspace_NonInteractiveDeclines(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	testenv.New(t)
	stubSubIDFiles(t)

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupRootlessDaemon()

	// testIOStreams is non-TTY, so CanPrompt is false.
	_, err := ensureIDMappedWorkspace(context.Background(), fake.Client,
		idmapHostConfig(root), []string{root}, testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive terminal")
	assert.Contains(t, err.Error(), root)
	assert.NotContains(t, err.Error(), idmapHelperName,
		"a headless run must not be handed a command it cannot run")
}

// TestEnsureIDMappedWorkspace_RemoteRootlessDaemonRefused: the view is a
// mount on the CLI's machine, computed from the CLI's subordinate ID tables
// — meaningless to a daemon running elsewhere. Falling through would rewrite
// the binds to local paths the daemon host auto-creates empty.
func TestEnsureIDMappedWorkspace_RemoteRootlessDaemonRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	t.Parallel()

	root := t.TempDir()
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupRootlessDaemon()
	fake.FakeAPI.DaemonHostFn = func() string { return "ssh://dev@build-box" }

	_, err := ensureIDMappedWorkspace(context.Background(), fake.Client,
		idmapHostConfig(root), []string{root}, testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh://dev@build-box")
	assert.Contains(t, err.Error(), "own host")
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

	_, err := ensureIDMappedWorkspace(context.Background(), fake.Client,
		idmapHostConfig(root), []string{root}, testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon unreachable")
}

// TestEnsureIDMappedViewsAtStart_NoLabelNoOp: containers created against a
// rootful daemon (or before this feature) carry no roots label, and the
// start path must leave them entirely alone.
func TestEnsureIDMappedViewsAtStart_NoLabelNoOp(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	t.Parallel()

	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	//nolint:exhaustruct // fixture: only the fields the start path reads matter
	fake.SetupContainerInspect("agent-1", container.Summary{
		ID:     "agent-1",
		Labels: map[string]string{cfg.LabelManaged(): "true"},
	})

	err := ensureIDMappedViewsAtStart(context.Background(), fake.Client,
		"agent-1", testIOStreams(), logger.Nop())
	require.NoError(t, err)
}

// TestEnsureIDMappedViewsAtStart_MissingViewFailsLoud: after a reboot the
// mounts are gone; a non-interactive start must refuse rather than let
// Docker bind the bare mount-point directory and hand the container an
// empty workspace.
func TestEnsureIDMappedViewsAtStart_MissingViewFailsLoud(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ID-mapped views are Linux-only")
	}
	testenv.New(t)
	stubSubIDFiles(t)

	root := t.TempDir()
	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	//nolint:exhaustruct // fixture: only the fields the start path reads matter
	fake.SetupContainerInspect("agent-1", container.Summary{
		ID: "agent-1",
		Labels: map[string]string{
			cfg.LabelManaged():     "true",
			consts.LabelIDMapRoots: fmt.Sprintf(`[%q]`, root),
		},
	})

	err := ensureIDMappedViewsAtStart(context.Background(), fake.Client,
		"agent-1", testIOStreams(), logger.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive terminal")
	assert.Contains(t, err.Error(), root)
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
