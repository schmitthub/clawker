package cpboot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// bootstrapFixture isolates auth/image/healthz seams so EnsureRunning
// unit tests stay fast and deterministic. Tests that want to exercise
// the full pipeline override individual fakes on the FakeAPI.
type bootstrapFixture struct {
	cfg   config.Config
	fake  *dockermocks.FakeClient
	calls *bootstrapCalls
}

type bootstrapCalls struct {
	auth    atomic.Int32
	image   atomic.Int32
	healthz atomic.Int32
	create  atomic.Int32
	start   atomic.Int32
	stop    atomic.Int32
	remove  atomic.Int32
}

// newBootstrapFixture installs stubs on the package-level seams that
// count invocations and return success. Tests override FakeAPI fields
// for Docker-side behavior. The returned reset func restores the
// production seams so concurrent test runs don't bleed state.
func newBootstrapFixture(t *testing.T) *bootstrapFixture {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)
	calls := &bootstrapCalls{}

	// Network inspect returns a valid clawker-net with IPAM so
	// DiscoverNetwork + ComputeStaticIP succeed.
	fake.FakeAPI.NetworkInspectFn = func(_ context.Context, name string, _ mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
		return mobyclient.NetworkInspectResult{
			Network: network.Inspect{
				Network: network.Network{
					Name:   name,
					ID:     "net-" + name,
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
					IPAM: network.IPAM{
						Config: []network.IPAMConfig{
							{
								Subnet:  netip.MustParsePrefix("172.20.0.0/16"),
								Gateway: netip.MustParseAddr("172.20.0.1"),
							},
						},
					},
				},
			},
		}, nil
	}

	// Default: no existing CP container.
	fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{}, nil
	}
	fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		calls.create.Add(1)
		return mobyclient.ContainerCreateResult{ID: "cp-id"}, nil
	}
	fake.FakeAPI.ContainerStartFn = func(_ context.Context, _ string, _ mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error) {
		calls.start.Add(1)
		return mobyclient.ContainerStartResult{}, nil
	}
	fake.FakeAPI.ContainerStopFn = func(_ context.Context, _ string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		calls.stop.Add(1)
		return mobyclient.ContainerStopResult{}, nil
	}
	fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, _ string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		calls.remove.Add(1)
		return mobyclient.ContainerRemoveResult{}, nil
	}
	fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		// Default: inspect for the managed-label jail check returns a
		// minimal managed container; individual tests override this when
		// they care about HostConfig.Mounts divergence detection.
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{},
			},
		}, nil
	}
	origAuth, origImage, origHealthz := ensureAuthFn, ensureCPImageFn, healthzFn
	ensureAuthFn = func() error {
		calls.auth.Add(1)
		return nil
	}
	ensureCPImageFn = func(_ context.Context, _ *docker.Client, _ *logger.Logger) error {
		calls.image.Add(1)
		return nil
	}
	healthzFn = func(_ context.Context, _ config.Config) error {
		calls.healthz.Add(1)
		return nil
	}

	t.Cleanup(func() {
		ensureAuthFn = origAuth
		ensureCPImageFn = origImage
		healthzFn = origHealthz
	})
	return &bootstrapFixture{cfg: cfg, fake: fake, calls: calls}
}

// testHostDirs returns HostDirs populated from the current test-env XDG
// dirs. Callable from any _test.go in this package — configmocks.
// NewIsolatedTestConfig sets the CLAWKER_*_DIR env vars via t.Setenv,
// so consts.{ConfigDir,DataDir,StateDir,CacheDir} resolve to the
// testenv paths.
func testHostDirs() HostDirs {
	return HostDirs{
		Config: consts.ConfigDir(),
		Data:   consts.DataDir(),
		State:  consts.StateDir(),
		Cache:  consts.CacheDir(),
	}
}

// testCPOpts returns CPContainerOpts wrapping testHostDirs.
func testCPOpts() CPContainerOpts { return CPContainerOpts{HostDirs: testHostDirs()} }

// hostDirs resolves the host-side XDG dirs from the isolated test env.
// configmocks.NewIsolatedTestConfig sets CLAWKER_*_DIR env vars via
// t.Setenv, so consts.{ConfigDir,DataDir,StateDir,CacheDir} return the
// testenv-backed paths rather than real user paths.
func (f *bootstrapFixture) hostDirs() HostDirs {
	return HostDirs{
		Config: consts.ConfigDir(),
		Data:   consts.DataDir(),
		State:  consts.StateDir(),
		Cache:  consts.CacheDir(),
	}
}

// ensureOpts returns a fully-populated EnsureOpts for the fixture.
func (f *bootstrapFixture) ensureOpts() EnsureOpts {
	return EnsureOpts{
		Docker:   f.fake.Client,
		Config:   f.cfg,
		Logger:   logger.Nop(),
		HostDirs: f.hostDirs(),
	}
}

// cpOpts returns CPContainerOpts for the fixture.
func (f *bootstrapFixture) cpOpts() CPContainerOpts {
	return CPContainerOpts{HostDirs: f.hostDirs()}
}

func TestEnsureRunning_HappyPath_CreatesContainer(t *testing.T) {
	f := newBootstrapFixture(t)

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)

	assert.Equal(t, int32(1), f.calls.auth.Load(), "auth ensured once")
	assert.Equal(t, int32(1), f.calls.image.Load(), "image ensured once")
	assert.Equal(t, int32(1), f.calls.create.Load(), "container created once")
	assert.Equal(t, int32(1), f.calls.start.Load(), "container started once")
	assert.Equal(t, int32(1), f.calls.healthz.Load(), "healthz polled once")
}

func TestEnsureRunning_AlreadyRunning_IsNoOp(t *testing.T) {
	f := newBootstrapFixture(t)

	// Pretend the CP container is already running with the expected mount
	// set — EnsureRunning should fast-path to healthz.
	wantCfg, err := BuildCPContainerConfig(f.cfg, f.cpOpts())
	require.NoError(t, err)

	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
			},
		}}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	require.NoError(t, EnsureRunning(t.Context(), f.ensureOpts()))
	assert.Zero(t, f.calls.create.Load(), "no create when already running")
	assert.Zero(t, f.calls.start.Load(), "no start when already running")
	assert.Equal(t, int32(1), f.calls.healthz.Load(), "healthz probed for running CP")
}

func TestEnsureRunning_ExistingStopped_StartsWithoutRecreate(t *testing.T) {
	f := newBootstrapFixture(t)

	wantCfg, err := BuildCPContainerConfig(f.cfg, f.cpOpts())
	require.NoError(t, err)

	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateExited,
				Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
			},
		}}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	require.NoError(t, EnsureRunning(t.Context(), f.ensureOpts()))
	assert.Zero(t, f.calls.create.Load(), "no create when only stopped")
	assert.Equal(t, int32(1), f.calls.start.Load(), "existing container started")
	assert.Zero(t, f.calls.remove.Load(), "no remove when mounts match")
}

func TestEnsureRunning_MountDivergence_RecreatesContainer(t *testing.T) {
	// INV-B2-006: any divergence from BuildCPContainerConfig's mount
	// spec forces recreation. Table exercises each divergence mode
	// hasMountDivergence checks: missing mount, RO/RW flip, Source
	// path change, and extra mount not in the want set.
	cfg := configmocks.NewIsolatedTestConfig(t)
	wantCfg, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	legacyRO := make([]mount.Mount, len(wantCfg.Mounts))
	copy(legacyRO, wantCfg.Mounts)
	for i := range legacyRO {
		if legacyRO[i].Target == consts.CPFirewallDataDir {
			legacyRO[i].ReadOnly = true
		}
	}
	movedSource := make([]mount.Mount, len(wantCfg.Mounts))
	copy(movedSource, wantCfg.Mounts)
	for i := range movedSource {
		if movedSource[i].Target == consts.CPFirewallDataDir {
			movedSource[i].Source = "/tmp/stale/path"
		}
	}
	extraMount := append([]mount.Mount(nil), wantCfg.Mounts...)
	extraMount = append(extraMount, mount.Mount{Type: mount.TypeBind, Source: "/legacy", Target: "/legacy"})

	cases := []struct {
		name   string
		mounts []mount.Mount
	}{
		{"missing firewall-data mount", []mount.Mount{
			{Type: mount.TypeBind, Source: "/anywhere", Target: consts.CPClawkerConfigDir, ReadOnly: true},
		}},
		{"RO where RW expected", legacyRO},
		{"divergent source path", movedSource},
		{"extra mount not in want set", extraMount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
				return mobyclient.ContainerListResult{Items: []container.Summary{
					{
						ID:     "divergent-cp",
						Names:  []string{"/" + consts.ContainerCP},
						State:  container.StateRunning,
						Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
					},
				}}, nil
			}
			mounts := tc.mounts
			f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
				return mobyclient.ContainerInspectResult{
					Container: container.InspectResponse{
						ID: id,
						Config: &container.Config{
							Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
						},
						HostConfig: &container.HostConfig{Mounts: mounts},
					},
				}, nil
			}

			require.NoError(t, EnsureRunning(t.Context(), f.ensureOpts()))
			assert.Equal(t, int32(1), f.calls.stop.Load(), "divergent container stopped")
			assert.Equal(t, int32(1), f.calls.remove.Load(), "divergent container removed")
			assert.Equal(t, int32(1), f.calls.create.Load(), "fresh container created")
		})
	}
}

func TestEnsureRunning_HealthzTimeout_SurfacesError(t *testing.T) {
	// /healthz never returns 200 — EnsureRunning must propagate the
	// timeout error rather than blocking indefinitely.
	f := newBootstrapFixture(t)
	sentinel := &CPHealthTimeoutError{Timeout: 5 * time.Millisecond, URL: "http://127.0.0.1:7080/healthz"}
	healthzFn = func(_ context.Context, _ config.Config) error {
		f.calls.healthz.Add(1)
		return sentinel
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	var got *CPHealthTimeoutError
	require.ErrorAs(t, err, &got)
	assert.Equal(t, sentinel.URL, got.URL)
	assert.Equal(t, int32(1), f.calls.create.Load(), "container still created before healthz")
}

func TestEnsureRunning_ConcurrentCallers_SingleCreate(t *testing.T) {
	// INV-B2-006: the package-level mutex must serialize overlapping
	// EnsureRunning calls. The fake blocks ContainerCreate on a
	// release-channel until every goroutine has entered EnsureRunning,
	// so without the mutex all N goroutines would reach create before
	// any call observed the freshly-created container via ContainerList.
	// The mutex forces them to run one at a time — the second onward
	// see the running container and fast-path to healthz.
	f := newBootstrapFixture(t)
	wantCfg, err := BuildCPContainerConfig(f.cfg, f.cpOpts())
	require.NoError(t, err)

	const goroutines = 5
	release := make(chan struct{})
	var listMu sync.Mutex
	created := false
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		listMu.Lock()
		defer listMu.Unlock()
		if !created {
			return mobyclient.ContainerListResult{}, nil
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
			},
		}}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		<-release
		listMu.Lock()
		created = true
		listMu.Unlock()
		return mobyclient.ContainerCreateResult{ID: "cp-id"}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = EnsureRunning(t.Context(), f.ensureOpts())
		}(i)
	}

	// Wait for at least one goroutine to reach the blocking create.
	// If the mutex were missing, more than one would be queued here.
	require.Eventually(t, func() bool {
		return f.calls.create.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond, "at least one create should reach the fake")

	// A brief settle to let any goroutines that bypassed the mutex
	// enqueue their own creates. Under a correct mutex, only one can
	// be blocked in create at a time — the rest are still upstream of
	// findCPContainer, waiting on ensureMu.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), f.calls.create.Load(), "ensureMu must serialize callers; extra creates indicate missing lock")

	close(release)
	wg.Wait()
	for i, e := range errs {
		require.NoError(t, e, "goroutine %d", i)
	}
	assert.Equal(t, int32(1), f.calls.create.Load(), "final create count after serialization must remain 1")
}

func TestEnsureRunning_NameConflictRecovery_NoSecondCreate(t *testing.T) {
	// Cross-process race: another bootstrapper created the CP between
	// findCPContainer and ContainerCreate. Docker returns "already in
	// use"; we recover by starting the existing container.
	f := newBootstrapFixture(t)
	wantCfg, err := BuildCPContainerConfig(f.cfg, f.cpOpts())
	require.NoError(t, err)

	// First list returns empty, second list (after name conflict) returns
	// the conflicting container so recovery can find it.
	var listCount atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCount.Add(1) == 1 {
			return mobyclient.ContainerListResult{}, nil
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "conflict-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateExited,
				Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
			},
		}}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		// Wrap cerrdefs.ErrConflict so the production code's
		// IsConflict gate matches — matches what the moby client
		// returns for HTTP 409 name-collision responses.
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	require.NoError(t, EnsureRunning(t.Context(), f.ensureOpts()))
	// The recovery path must NOT re-issue a second ContainerCreate after
	// picking up the pre-existing container — that's the whole point of
	// the test name. The conflict happens during the first attempted
	// create; recovery reuses the found container and only starts it.
	assert.Equal(t, int32(1), f.calls.create.Load(), "no second create after conflict recovery")
	assert.GreaterOrEqual(t, f.calls.start.Load(), int32(1), "recovered container started at least once")
}

func TestStop_MissingContainer_IsNoOp(t *testing.T) {
	f := newBootstrapFixture(t)

	require.NoError(t, Stop(t.Context(), f.fake.Client))
	assert.Zero(t, f.calls.stop.Load())
	assert.Zero(t, f.calls.remove.Load())
}

func TestStop_ExistingContainer_StopsAndRemoves(t *testing.T) {
	f := newBootstrapFixture(t)
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
			},
		}}, nil
	}

	require.NoError(t, Stop(t.Context(), f.fake.Client))
	assert.Equal(t, int32(1), f.calls.stop.Load())
	assert.Equal(t, int32(1), f.calls.remove.Load())
}

func TestWaitForCPHealthz_ContextCancelled_ReturnsCtxErr(t *testing.T) {
	// The poller respects context cancellation before the healthCheck
	// deadline. Immediately-cancelled context short-circuits the first
	// iteration.
	cfg := configmocks.NewIsolatedTestConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitForCPHealthz(ctx, cfg)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWaitForCPHealthz_Timeout_ReturnsTypedError(t *testing.T) {
	// An httptest server that always returns 503 with a diagnostic body
	// deterministically exercises the timeout path. The poller's own
	// deadline fires before the context deadline, so we expect the
	// typed *CPHealthTimeoutError — anything else (including bare
	// context.DeadlineExceeded) means a regression in the last-probe
	// capture or the loop ordering.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("hydra: not ready"))
	}))
	defer srv.Close()
	port, err := portFromTestServer(srv)
	require.NoError(t, err)

	cfg := configmocks.NewFromString("", fmt.Sprintf("control_plane:\n  health_port: %d\n", port))

	// Drive the inner deadline via ctx so the test completes fast while
	// still proving the deadline-path returns the typed error.
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	err = waitForCPHealthz(ctx, cfg)
	require.Error(t, err)
	var timeoutErr *CPHealthTimeoutError
	require.ErrorAs(t, err, &timeoutErr, "must return *CPHealthTimeoutError, got %T: %v", err, err)
	assert.Contains(t, timeoutErr.URL, "/healthz")
	assert.Equal(t, http.StatusServiceUnavailable, timeoutErr.LastStatus)
	assert.Contains(t, timeoutErr.LastBody, "hydra: not ready")
}

// portFromTestServer extracts the TCP port bound by an httptest server.
func portFromTestServer(srv *httptest.Server) (int, error) {
	u, err := url.Parse(srv.URL)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Port())
}

func TestBuildCPContainerConfig_FirewallDataMountedRW(t *testing.T) {
	// INV-B2-006 complement: the CP must mount FirewallDataSubdir RW.
	cfg := configmocks.NewIsolatedTestConfig(t)
	cpCfg, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)

	found := false
	for _, m := range cpCfg.Mounts {
		if m.Target == consts.CPFirewallDataDir {
			found = true
			assert.False(t, m.ReadOnly, "firewall data must be RW for the CP (sole writer)")
			break
		}
	}
	assert.True(t, found, "CP must mount the firewall data dir")
}

func TestBuildCPContainerConfig_RestartPolicyOnFailure(t *testing.T) {
	// INV-B2-007 guard: CP must not auto-restart on graceful exit.
	cfg := configmocks.NewIsolatedTestConfig(t)
	cpCfg, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	assert.Equal(t, container.RestartPolicyOnFailure, cpCfg.RestartPolicy.Name)
	assert.Equal(t, consts.CPMaxRestartRetries, cpCfg.RestartPolicy.MaximumRetryCount)
}

func TestBuildCPContainerConfig_ClawkerNetAttachment(t *testing.T) {
	// INV-B2-014: CP container attaches to clawker-net so it can reach
	// Envoy and CoreDNS by their internal IPs.
	cfg := configmocks.NewIsolatedTestConfig(t)
	cpCfg, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	assert.Equal(t, consts.Network, cpCfg.NetworkName)
}
