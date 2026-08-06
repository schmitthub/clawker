package firewall

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
)

// cgroupTestID is a canonical 64-char lowercase hex container ID.
const cgroupTestID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// cgroupTestID2 is a second canonical ID for cache-reuse assertions.
const cgroupTestID2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// rootlessSlice mirrors the layout a rootless systemd daemon produces: the
// scope parent sits at a uid-dependent depth under the user slice.
const rootlessSlice = "user.slice/user-1003.slice/user@1003.service/user.slice"

func mkCgroupDir(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
}

func newTestPathResolver(driver, root string) *cgroupPathResolver {
	return &cgroupPathResolver{driver: driver, root: root, mu: sync.Mutex{}, parent: ""}
}

// TestCgroupPathResolver_ConventionalFastPath pins the rootful layouts: a
// scope under system.slice (systemd) or a bare ID under docker/ (cgroupfs)
// resolves without any discovery walk.
func TestCgroupPathResolver_ConventionalFastPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		driver string
		rel    string
	}{
		{driver: "systemd", rel: "system.slice/docker-" + cgroupTestID + ".scope"},
		{driver: "cgroupfs", rel: "docker/" + cgroupTestID},
	}
	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			want := filepath.Join(root, tc.rel)
			mkCgroupDir(t, want)

			got, err := newTestPathResolver(tc.driver, root).path(cgroupTestID)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

// TestCgroupPathResolver_DiscoversRootlessSystemdLayout is the rootless bug
// this resolver exists for: the daemon parks scopes under the user slice at
// a uid-dependent depth no constant can express. Discovery must find the
// scope by its own directory name and cache the parent for the next
// container of the same daemon.
func TestCgroupPathResolver_DiscoversRootlessSystemdLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, rootlessSlice)
	first := filepath.Join(parent, "docker-"+cgroupTestID+".scope")
	mkCgroupDir(t, first)

	r := newTestPathResolver("systemd", root)
	got, err := r.path(cgroupTestID)
	require.NoError(t, err)
	assert.Equal(t, first, got)
	assert.Equal(t, parent, r.parent, "discovered parent must be cached")

	// A sibling container of the same daemon resolves through the cache.
	second := filepath.Join(parent, "docker-"+cgroupTestID2+".scope")
	mkCgroupDir(t, second)
	got, err = r.path(cgroupTestID2)
	require.NoError(t, err)
	assert.Equal(t, second, got)
}

// TestCgroupPathResolver_DiscoversRootlessCgroupfsLayout covers the cgroupfs
// sibling: the container's cgroup is a bare-ID directory wherever the daemon
// put its docker parent.
func TestCgroupPathResolver_DiscoversRootlessCgroupfsLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := filepath.Join(root, rootlessSlice, "docker", cgroupTestID)
	mkCgroupDir(t, want)

	got, err := newTestPathResolver("cgroupfs", root).path(cgroupTestID)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestCgroupPathResolver_MissingCgroupErrors: Docker says the container is
// alive, so a cgroup that exists nowhere in the hierarchy is a fault to
// surface — fabricating a conventional path would just move the ENOENT to
// eBPF attach time with less context.
func TestCgroupPathResolver_MissingCgroupErrors(t *testing.T) {
	t.Parallel()

	_, err := newTestPathResolver("systemd", t.TempDir()).path(cgroupTestID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cgroup directory")
	assert.Contains(t, err.Error(), "docker-"+cgroupTestID+".scope")
}

// TestNewContainerResolverAt_ResolvesRootlessCgroupPath drives the full
// resolver: friendly ref → Docker → canonical ID → discovered rootless
// cgroup path.
func TestNewContainerResolverAt_ResolvesRootlessCgroupPath(t *testing.T) {
	t.Parallel()

	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	fake.FakeAPI.ContainerInspectFn = func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		//nolint:exhaustruct // moby wire types: only the fields the resolver reads matter
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: cgroupTestID,
				Config: &container.Config{
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
				},
			},
		}, nil
	}

	root := t.TempDir()
	want := filepath.Join(root, rootlessSlice, "docker-"+cgroupTestID+".scope")
	mkCgroupDir(t, want)

	resolve := newContainerResolverAt(fake.Client, "systemd", root)
	id, cgroupPath, exists, err := resolve(t.Context(), "clawker.myapp.dev")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, cgroupTestID, id)
	assert.Equal(t, want, cgroupPath)
}

// TestNewContainerResolverAt_CanonicalIDSkipsDockerRoundTrip: a canonical
// long-hex ref short-circuits inside ResolveContainerID and never touches
// Docker. ContainerInspect is left unset so a regression that drops the
// short-circuit would panic with "not implemented".
func TestNewContainerResolverAt_CanonicalIDSkipsDockerRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	fake.FakeAPI.ContainerInspectFn = nil

	root := t.TempDir()
	want := filepath.Join(root, "docker", cgroupTestID)
	mkCgroupDir(t, want)

	resolve := newContainerResolverAt(fake.Client, "cgroupfs", root)
	id, cgroupPath, exists, err := resolve(t.Context(), cgroupTestID)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, cgroupTestID, id)
	assert.Equal(t, want, cgroupPath)
	assert.NotContains(t, fake.FakeAPI.Calls, "ContainerInspect")
}

// TestNewContainerResolverAt_MissingCgroupSurfacesError: Docker resolves the
// container but no cgroup directory exists anywhere under the root — the
// resolver must fail loud, never hand back a fabricated path.
func TestNewContainerResolverAt_MissingCgroupSurfacesError(t *testing.T) {
	t.Parallel()

	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	fake.FakeAPI.ContainerInspectFn = nil

	resolve := newContainerResolverAt(fake.Client, "systemd", t.TempDir())
	id, cgroupPath, exists, err := resolve(t.Context(), cgroupTestID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locating cgroup for container")
	assert.False(t, exists)
	assert.Empty(t, id)
	assert.Empty(t, cgroupPath)
}
