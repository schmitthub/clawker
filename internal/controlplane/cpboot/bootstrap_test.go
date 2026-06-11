package cpboot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	dockerimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
	image     atomic.Int32
	healthz   atomic.Int32
	clockSync atomic.Int32
	create    atomic.Int32
	start     atomic.Int32
	stop      atomic.Int32
	remove    atomic.Int32
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

	// Network inspect returns a valid clawker network with IPAM so
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
		// minimal managed container. Mount-spec reconciliation was retired
		// (see spec §INV-B2-006 History) so tests no longer stub mounts.
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
	origImage, origHealthz, origClockSync := ensureCPImageFn, healthzFn, clockSyncFn
	ensureCPImageFn = func(_ context.Context, _ *docker.Client, _ *logger.Logger) (string, error) {
		calls.image.Add(1)
		return cpImageRef(), nil
	}
	healthzFn = func(_ context.Context, _ *docker.Client, _ config.Config) error {
		calls.healthz.Add(1)
		return nil
	}
	// Stub the clock-sync gate (real impl dials the CP's GetSystemTime).
	clockSyncFn = func(_ context.Context, _ config.Config, _ *logger.Logger) error {
		calls.clockSync.Add(1)
		return nil
	}

	t.Cleanup(func() {
		ensureCPImageFn = origImage
		healthzFn = origHealthz
		clockSyncFn = origClockSync
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
func testCPOpts() CPContainerOpts {
	return CPContainerOpts{HostDirs: testHostDirs(), Image: cpImageRef()}
}

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
	return CPContainerOpts{HostDirs: f.hostDirs(), Image: cpImageRef()}
}

// managedImageInspect constructs an ImageInspectResult that whail's
// label-jail accepts as managed, with the supplied Created timestamp
// (as both the InspectResponse.Created field and the
// org.opencontainers.image.created LABEL). Used by the
// name-conflict-recovery tests that exercise the
// timestamp-comparison branch of recoverFromNameConflict.
func managedImageInspect(cfg config.Config, created string) mobyclient.ImageInspectResult {
	return mobyclient.ImageInspectResult{
		InspectResponse: dockerimage.InspectResponse{
			Created: created,
			Config: &dockerspec.DockerOCIImageConfig{
				ImageConfig: ocispec.ImageConfig{
					Labels: map[string]string{
						cfg.LabelManaged():       cfg.ManagedLabelValue(),
						consts.LabelImageCreated: created,
					},
				},
			},
		},
	}
}

// cpLabels returns the minimum label set a freshly-built CP container
// would carry — managed + the content-derived binary SHA. Tests stubbing
// ContainerList put this on the fake summary so EnsureRunning's drift
// compare matches and adopts the existing container instead of
// force-removing it.
func (f *bootstrapFixture) cpLabels() map[string]string {
	full, _ := cpBinaryHash()
	return map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: full,
	}
}

func TestEnsureRunning_HappyPath_CreatesContainer(t *testing.T) {
	f := newBootstrapFixture(t)

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)

	assert.Equal(t, int32(1), f.calls.image.Load(), "image ensured once")
	assert.Equal(t, int32(1), f.calls.create.Load(), "container created once")
	assert.Equal(t, int32(1), f.calls.start.Load(), "container started once")
	assert.Equal(t, int32(1), f.calls.healthz.Load(), "healthz polled once")
	assert.Equal(t, int32(1), f.calls.clockSync.Load(), "clock-sync gate runs on the create path")
}

// TestEnsureRunning_ForwardsSecurityOptToHostConfig pins the
// createCPContainer wiring that copies cpConfig.SecurityOpt into the
// moby HostConfig. The companion BuildCPContainerConfig test only
// covers the upstream struct; without this assertion, a future change
// could keep SecurityOpt populated on the config layer while dropping
// the HostConfig assignment, silently re-enabling docker-default
// AppArmor and re-introducing the bpffs mkdir EPERM at eBPF Load.
func TestEnsureRunning_ForwardsSecurityOptToHostConfig(t *testing.T) {
	f := newBootstrapFixture(t)

	var captured *container.HostConfig
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, opts mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		captured = opts.HostConfig
		return mobyclient.ContainerCreateResult{ID: "cp-id"}, nil
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	require.NotNil(t, captured, "ContainerCreate must receive a HostConfig")
	assert.Contains(t, captured.SecurityOpt, "apparmor=unconfined",
		"createCPContainer must forward cpConfig.SecurityOpt into HostConfig")
}

func TestEnsureRunning_AlreadyRunning_IsNoOp(t *testing.T) {
	f := newBootstrapFixture(t)

	// SHA-match adoption reads labels straight off the list summary; no
	// ContainerInspect call should happen, so leaving the default inspect
	// stub in place (returns labels without consts.LabelCPBinarySHA) keeps
	// the test honest — if a refactor moves the SHA read to Inspect, the
	// adoption will silently break and create will fire.
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{
				ID:     "cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: f.cpLabels(),
			},
		}}, nil
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Zero(t, f.calls.create.Load(), "no create when already running")
	assert.Zero(t, f.calls.start.Load(), "no start when already running")
	assert.Equal(t, int32(1), f.calls.healthz.Load(), "healthz probed for running CP")
	assert.Equal(t, int32(1), f.calls.clockSync.Load(), "clock-sync gate runs on the adopt path")
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
				Labels: f.cpLabels(),
			},
		}}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: f.cpLabels(),
				},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	err = EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Zero(t, f.calls.create.Load(), "no create when only stopped")
	assert.Equal(t, int32(1), f.calls.start.Load(), "existing container started")
	assert.Zero(t, f.calls.remove.Load(), "no remove when binary SHA matches")
}

func TestEnsureRunning_HealthzTimeout_SurfacesError(t *testing.T) {
	// /healthz never returns 200 — EnsureRunning must propagate the
	// timeout error rather than blocking indefinitely.
	f := newBootstrapFixture(t)
	sentinel := &CPHealthTimeoutError{Timeout: 5 * time.Millisecond, URL: "http://" + consts.LoopbackIPv4 + ":7080/healthz"}
	healthzFn = func(_ context.Context, _ *docker.Client, _ config.Config) error {
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

// TestEnsureRunning_ClockSyncFailure_SurfacesError pins the readiness contract
// end-to-end: a green /healthz with a CP clock that never converges must make
// EnsureRunning FAIL, not succeed. The other clock-sync tests stub the gate to
// return nil and only assert it was invoked; this one stubs it to error and
// proves cpReady propagates that error out of EnsureRunning (a reorder that ran
// the gate but swallowed its error, or returned nil after healthz, would regress
// the whole point of gating create on clock sync).
func TestEnsureRunning_ClockSyncFailure_SurfacesError(t *testing.T) {
	f := newBootstrapFixture(t)
	// /healthz is green (fixture default), but the clock never catches up.
	clockSyncFn = func(_ context.Context, _ config.Config, _ *logger.Logger) error {
		f.calls.clockSync.Add(1)
		return fmt.Errorf("cp clock never caught up to host (test sentinel)")
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	assert.ErrorContains(t, err, "cp clock never caught up to host (test sentinel)",
		"clock-sync gate failure must propagate out of EnsureRunning")
	assert.Equal(t, int32(1), f.calls.clockSync.Load(), "clock-sync gate ran")
	assert.Equal(t, int32(1), f.calls.create.Load(), "container created before the clock-sync gate")
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
				Labels: f.cpLabels(),
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
	// Cross-process race with matching binary SHAs: another bootstrapper
	// (running the same clawker build) created the CP between
	// findCPContainer and ContainerCreate. Docker returns "already in
	// use"; recovery sees the peer container carries our exact binary SHA
	// and adopts it without a second create.
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
				Labels: f.cpLabels(),
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
				ID:         id,
				Image:      cpImageRef(),
				State:      &container.State{Status: container.StateExited},
				Config:     &container.Config{Labels: f.cpLabels()},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	err = EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	// The recovery path must NOT re-issue a second ContainerCreate after
	// picking up the pre-existing container — that's the whole point of
	// the test name. The conflict happens during the first attempted
	// create; recovery sees a SHA-match and adopts the recovered
	// container, only starting it.
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

// TestCPTerminalError pins the fail-fast feedback contract of the healthz
// wait's container-state discrimination: terminally-exited and removed
// containers abort the wait with typed errors; everything transient
// (running, mid-restart, Docker hiccups) keeps the loop polling, with
// lookup failures surfaced on the second return for timeout diagnostics.
func TestCPTerminalError(t *testing.T) {
	newClient := func(t *testing.T, state *container.State, inspectErr error) *docker.Client {
		t.Helper()
		cfg := configmocks.NewIsolatedTestConfig(t)
		fake := dockermocks.NewFakeClient(cfg)
		summaryState := container.StateRunning
		if state != nil {
			summaryState = state.Status
		}
		fake.SetupContainerList(container.Summary{
			ID:     "cp-id",
			Names:  []string{"/" + consts.ContainerCP},
			State:  summaryState,
			Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
		})
		fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			if inspectErr != nil {
				return mobyclient.ContainerInspectResult{}, inspectErr
			}
			return mobyclient.ContainerInspectResult{
				Container: container.InspectResponse{
					ID: id,
					Config: &container.Config{
						Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
					},
					HostConfig: &container.HostConfig{},
					State:      state,
				},
			}, nil
		}
		return fake.Client
	}

	t.Run("exited and not restarting is terminal", func(t *testing.T) {
		dc := newClient(t, &container.State{Status: container.StateExited, ExitCode: 1}, nil)
		terminalErr, lookupErr := cpTerminalError(t.Context(), dc)
		require.NoError(t, lookupErr)
		var exitedErr *CPExitedError
		require.ErrorAs(t, terminalErr, &exitedErr)
		assert.Equal(t, 1, exitedErr.ExitCode, "container exit code must propagate for operator triage")
	})

	t.Run("removed container is terminal", func(t *testing.T) {
		cfg := configmocks.NewIsolatedTestConfig(t)
		fake := dockermocks.NewFakeClient(cfg)
		fake.SetupContainerList() // empty list — CP container gone
		terminalErr, lookupErr := cpTerminalError(t.Context(), fake.Client)
		require.NoError(t, lookupErr)
		var goneErr *CPGoneError
		require.ErrorAs(t, terminalErr, &goneErr,
			"a removed CP must abort the wait instead of burning the budget on transport errors")
	})

	t.Run("running keeps polling", func(t *testing.T) {
		dc := newClient(t, &container.State{Status: container.StateRunning}, nil)
		terminalErr, lookupErr := cpTerminalError(t.Context(), dc)
		assert.NoError(t, terminalErr)
		assert.NoError(t, lookupErr)
	})

	t.Run("mid-restart keeps polling", func(t *testing.T) {
		dc := newClient(t, &container.State{Status: container.StateExited, Restarting: true, ExitCode: 1}, nil)
		terminalErr, lookupErr := cpTerminalError(t.Context(), dc)
		assert.NoError(t, terminalErr)
		assert.NoError(t, lookupErr)
	})

	t.Run("list error keeps polling and surfaces lookup error", func(t *testing.T) {
		errHiccup := errors.New("docker hiccup")
		cfg := configmocks.NewIsolatedTestConfig(t)
		fake := dockermocks.NewFakeClient(cfg)
		fake.SetupContainerListError(errHiccup)
		terminalErr, lookupErr := cpTerminalError(t.Context(), fake.Client)
		assert.NoError(t, terminalErr)
		assert.ErrorIs(t, lookupErr, errHiccup)
	})

	t.Run("inspect error keeps polling and surfaces lookup error", func(t *testing.T) {
		errHiccup := errors.New("docker hiccup")
		dc := newClient(t, &container.State{Status: container.StateExited, ExitCode: 1}, errHiccup)
		terminalErr, lookupErr := cpTerminalError(t.Context(), dc)
		assert.NoError(t, terminalErr)
		assert.ErrorIs(t, lookupErr, errHiccup)
	})
}

// TestWaitForCPHealthz_ExitedContainer_FailsFast pins the loop-level
// fast-fail: with healthz unreachable and the CP container terminally
// exited, the wait must return the typed *CPExitedError well before the
// budget elapses (zero-value throttle state means the very first failed
// probe triggers the container-state check) instead of burning the full
// healthz budget on a generic timeout.
func TestWaitForCPHealthz_ExitedContainer_FailsFast(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)
	fake.SetupContainerList(container.Summary{
		ID:     "cp-id",
		Names:  []string{"/" + consts.ContainerCP},
		State:  container.StateExited,
		Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
	})
	fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
				},
				HostConfig: &container.HostConfig{},
				State:      &container.State{Status: container.StateExited, ExitCode: 1},
			},
		}, nil
	}
	// Port 1 on loopback: nothing listens, so every healthz probe fails
	// at the transport layer (the branch that runs the state check).
	cfgUnreachable := configmocks.NewFromString("", "control_plane:\n  health_port: 1\n")

	start := time.Now()
	err := waitForCPHealthz(t.Context(), fake.Client, cfgUnreachable)
	var exitedErr *CPExitedError
	require.ErrorAs(t, err, &exitedErr)
	assert.Equal(t, 1, exitedErr.ExitCode)
	assert.Less(t, time.Since(start), 10*time.Second,
		"exited container must abort the wait long before the healthz budget elapses")
}

func TestWaitForCPHealthz_ContextCancelled_ReturnsCtxErr(t *testing.T) {
	// The poller respects context cancellation before the healthCheck
	// deadline. Immediately-cancelled context short-circuits the first
	// iteration.
	cfg := configmocks.NewIsolatedTestConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitForCPHealthz(ctx, nil, cfg)
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
	err = waitForCPHealthz(ctx, nil, cfg)
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
	// INV-B2-011 complement: Envoy + CoreDNS mount FirewallDataSubdir
	// read-only; the CP itself (sole authoritative writer of egress
	// rules, MITM CA, per-domain certs) must mount it read-write.
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
	// INV-B2-014: CP container attaches to the clawker network so it can reach
	// Envoy and CoreDNS by their internal IPs.
	cfg := configmocks.NewIsolatedTestConfig(t)
	cpCfg, err := BuildCPContainerConfig(cfg, testCPOpts())
	require.NoError(t, err)
	assert.Equal(t, consts.Network, cpCfg.NetworkName)
}

// TestEnsureCPImage_CacheHitOnSameBinary asserts that a second
// invocation with the same embedded binaries resolves the content-derived
// tag, finds the existing image via ImageInspect, and short-circuits —
// no second ImageBuild fires. This is the "rebuild only on real change"
// guarantee that fixes the original silent-staleness regression.
func TestEnsureCPImage_CacheHitOnSameBinary(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)

	// Stage non-empty embedded binaries so ensureCPImage isn't blocked
	// by the "binary not embedded" guard. Restore on cleanup so other
	// tests in this package see the production embed.
	origCP, origEBPF := ClawkerCPBinary, EBPFManagerBinary
	ClawkerCPBinary = []byte("stub-cp-binary-v1")
	EBPFManagerBinary = []byte("stub-ebpf-binary-v1")
	t.Cleanup(func() {
		ClawkerCPBinary = origCP
		EBPFManagerBinary = origEBPF
	})

	wantTag := cpImageRef()
	var inspectCalls, buildCalls atomic.Int32
	// Cache miss on first ImageInspect (NotFound), then succeed on the
	// second so the second ensureCPImage call returns without rebuilding.
	// Chain to dockermocks' default ImageInspectFn so the success path
	// returns labels matching this Engine's managed-label key (the
	// whailtest.ManagedImageInspect helper hardcodes a test prefix that
	// the production-shaped Engine treats as unmanaged).
	defaultInspect := fake.FakeAPI.ImageInspectFn
	fake.FakeAPI.ImageInspectFn = func(ctx context.Context, ref string, opts ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		require.Equal(t, wantTag, ref, "ImageInspect must query the content-derived tag, got %q", ref)
		if inspectCalls.Add(1) == 1 {
			return mobyclient.ImageInspectResult{}, cerrdefs.ErrNotFound
		}
		return defaultInspect(ctx, ref, opts...)
	}
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts mobyclient.ImageBuildOptions) (mobyclient.ImageBuildResult, error) {
		buildCalls.Add(1)
		require.Contains(t, opts.Tags, wantTag, "ImageBuild must be tagged with cpImageRef, got %v", opts.Tags)
		return mobyclient.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	// Prune helper lists then iterates RepoTags — empty list keeps it a
	// no-op so the test isolates the cache-hit behavior.
	fake.FakeAPI.ImageListFn = func(_ context.Context, _ mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
		return mobyclient.ImageListResult{}, nil
	}

	// `wantTag == ensureCPImage(...)` would be tautological — both
	// expressions evaluate the same pure cpImageRef() against the same
	// stubbed embedded bytes. The load-bearing assertions are the inspect-
	// ref check inside the stub (proves prod code resolves the content-
	// derived tag) and the build-call counter (proves the second call
	// short-circuits).
	_, err := ensureCPImage(t.Context(), fake.Client, logger.Nop())
	require.NoError(t, err)

	_, err = ensureCPImage(t.Context(), fake.Client, logger.Nop())
	require.NoError(t, err)

	assert.Equal(t, int32(1), buildCalls.Load(), "second call must hit the ImageInspect cache and skip ImageBuild")
}

// TestEnsureCPImage_RebuildsOnBinaryChange asserts that mutating the
// embedded binaries changes the resolved tag, the new tag misses on
// ImageInspect, and ImageBuild fires for the new tag — proving the
// content-derived identity is the actual gate.
func TestEnsureCPImage_RebuildsOnBinaryChange(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)

	origCP, origEBPF := ClawkerCPBinary, EBPFManagerBinary
	ClawkerCPBinary = []byte("stub-cp-binary-v1")
	EBPFManagerBinary = []byte("stub-ebpf-binary-v1")
	t.Cleanup(func() {
		ClawkerCPBinary = origCP
		EBPFManagerBinary = origEBPF
	})

	firstTag := cpImageRef()
	var built []string
	fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		return mobyclient.ImageInspectResult{}, cerrdefs.ErrNotFound
	}
	fake.FakeAPI.ImageBuildFn = func(_ context.Context, _ io.Reader, opts mobyclient.ImageBuildOptions) (mobyclient.ImageBuildResult, error) {
		built = append(built, opts.Tags...)
		return mobyclient.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	fake.FakeAPI.ImageListFn = func(_ context.Context, _ mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
		return mobyclient.ImageListResult{}, nil
	}

	_, err := ensureCPImage(t.Context(), fake.Client, logger.Nop())
	require.NoError(t, err)

	// Swap the embedded CP binary — the resolved tag must change.
	ClawkerCPBinary = []byte("stub-cp-binary-v2-different-content")
	secondTag := cpImageRef()
	require.NotEqual(t, firstTag, secondTag, "swapping the embedded binary must change cpImageRef")

	gotTag, err := ensureCPImage(t.Context(), fake.Client, logger.Nop())
	require.NoError(t, err)
	assert.Equal(t, secondTag, gotTag)
	assert.Equal(t, []string{firstTag, secondTag}, built,
		"ImageBuild must fire for both tags — content-derived identity gates rebuild")
}

// TestEnsureRunning_RecreatesStoppedDriftedContainer pins the
// post-drain + host-rebuild scenario described in the original failure
// mode: AgentWatcher's drain-to-zero shutdown left a stopped CP
// container on disk; the operator then rebuilt the host clawker binary
// (new binary SHA). Next `firewall up` must NOT start the stale stopped
// container — it must force-remove and recreate, otherwise the new
// mount/env spec from the rebuilt binary never reaches the running CP.
func TestEnsureRunning_RecreatesStoppedDriftedContainer(t *testing.T) {
	f := newBootstrapFixture(t)
	wantCfg, err := BuildCPContainerConfig(f.cfg, f.cpOpts())
	require.NoError(t, err)

	staleLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "stale-deadbeef-from-previous-build",
	}
	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCalls.Add(1) == 1 {
			return mobyclient.ContainerListResult{Items: []container.Summary{{
				ID:     "stale-cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateExited,
				Labels: staleLabels,
			}}}, nil
		}
		return mobyclient.ContainerListResult{}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:         id,
				Config:     &container.Config{Labels: staleLabels},
				HostConfig: &container.HostConfig{Mounts: wantCfg.Mounts},
			},
		}, nil
	}

	err = EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Equal(t, int32(1), f.calls.remove.Load(),
		"stopped + drifted container must be force-removed, not started")
	assert.Equal(t, int32(1), f.calls.create.Load(),
		"container must be recreated, not adopted by ContainerStart")
}

// TestEnsureRunning_NameConflict_TheirsNewer_AdoptsPeer pins the
// cross-process race where a peer bootstrapper running a NEWER clawker
// build won the ContainerCreate race. Our process has an older binary;
// the peer's container's image is newer per
// org.opencontainers.image.created. Recovery must adopt the peer's
// container (start if stopped) rather than force-removing it — the
// newer binary is the source of truth.
func TestEnsureRunning_NameConflict_TheirsNewer_AdoptsPeer(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-newerpeer123456"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-from-a-newer-clawker-build",
	}

	// First list (pre-create) returns empty so EnsureRunning falls
	// through to ContainerCreate. The create returns Conflict;
	// findCPContainer is re-run from recovery and must find the peer.
	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCalls.Add(1) == 1 {
			return mobyclient.ContainerListResult{}, nil
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{{
			ID:     "peer-cp-id",
			Names:  []string{"/" + consts.ContainerCP},
			State:  container.StateExited,
			Labels: peerLabels,
		}}}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Status: container.StateExited},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	// Our image was built before; peer's image was built one minute
	// later. Recovery must defer to the newer build.
	const oursCreated = "2026-05-21T10:00:00Z"
	const theirsCreated = "2026-05-21T10:01:00Z"
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		switch ref {
		case ourTag:
			return managedImageInspect(f.cfg, oursCreated), nil
		case peerImageRef:
			return managedImageInspect(f.cfg, theirsCreated), nil
		}
		return mobyclient.ImageInspectResult{}, fmt.Errorf("unexpected image ref %q", ref)
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Equal(t, int32(1), f.calls.create.Load(),
		"only the initial (conflict-ing) create attempt — no retry when peer wins")
	assert.Zero(t, f.calls.remove.Load(),
		"newer peer container must NOT be force-removed")
	assert.GreaterOrEqual(t, f.calls.start.Load(), int32(1),
		"adopted peer container must be started")
}

// TestEnsureRunning_NameConflict_OursNewer_ReplacesPeer pins the
// reverse race: peer bootstrapper running an OLDER clawker build won
// ContainerCreate first. Our binary is newer per
// org.opencontainers.image.created. Recovery must force-remove the
// peer's stale container and signal retry; the retry's create then
// fires successfully against the now-empty name.
func TestEnsureRunning_NameConflict_OursNewer_ReplacesPeer(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-olderpeer987654"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-from-an-older-clawker-build",
	}

	// First list (pre-create) empty → create fires, returns conflict.
	// Second list (from recovery's findCPContainer) returns the peer.
	// Third list (post-remove, if any) returns empty — but recovery
	// returns errCPRecoveryRetry which the create loop catches and
	// retries; the retry's create must NOT see a duplicate.
	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCalls.Add(1) == 2 {
			return mobyclient.ContainerListResult{Items: []container.Summary{{
				ID:     "peer-cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: peerLabels,
			}}}, nil
		}
		return mobyclient.ContainerListResult{}, nil
	}

	// Track each create attempt; first returns Conflict, second succeeds.
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		n := f.calls.create.Add(1)
		if n == 1 {
			return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
		}
		return mobyclient.ContainerCreateResult{ID: "fresh-cp-id"}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Running: true, Status: container.StateRunning},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	const oursCreated = "2026-05-21T10:01:00Z"
	const theirsCreated = "2026-05-21T10:00:00Z"
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		switch ref {
		case ourTag:
			return managedImageInspect(f.cfg, oursCreated), nil
		case peerImageRef:
			return managedImageInspect(f.cfg, theirsCreated), nil
		}
		return mobyclient.ImageInspectResult{}, fmt.Errorf("unexpected image ref %q", ref)
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Equal(t, int32(1), f.calls.remove.Load(),
		"older peer container must be force-removed")
	assert.Equal(t, int32(2), f.calls.create.Load(),
		"two create attempts: first conflicts, retry succeeds after remove")
}

// TestPruneStaleCPImages_KeepsKeepTagAndUnrelated verifies the three
// behavioral guarantees: (a) keepTag survives, (b) only the
// clawker-controlplane: prefix is in scope (unrelated repos untouched),
// (c) other clawker-controlplane:bin-* tags get removed.
func TestPruneStaleCPImages_KeepsKeepTagAndUnrelated(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)

	keepTag := consts.CPImageRepo + ":bin-keep1234567890"
	staleCPTag := consts.CPImageRepo + ":bin-stale987654321"
	legacyLatestTag := consts.CPImageRepo + ":latest"
	unrelatedTag := "redis:7-alpine"

	fake.FakeAPI.ImageListFn = func(_ context.Context, _ mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
		return mobyclient.ImageListResult{Items: []dockerimage.Summary{
			{ID: "sha256:keepimg", RepoTags: []string{keepTag}},
			{ID: "sha256:staleimg", RepoTags: []string{staleCPTag}},
			{ID: "sha256:legacyimg", RepoTags: []string{legacyLatestTag}},
			{ID: "sha256:redisimg", RepoTags: []string{unrelatedTag}},
		}}, nil
	}

	var removed []string
	fake.FakeAPI.ImageRemoveFn = func(_ context.Context, ref string, _ mobyclient.ImageRemoveOptions) (mobyclient.ImageRemoveResult, error) {
		removed = append(removed, ref)
		return mobyclient.ImageRemoveResult{}, nil
	}

	pruneStaleCPImages(t.Context(), fake.Client, keepTag, logger.Nop())

	assert.ElementsMatch(t, []string{staleCPTag, legacyLatestTag}, removed,
		"prune must remove stale + legacy CP tags but leave keepTag and unrelated repos alone")
}

// TestPruneStaleCPImages_ListFailure_Degrades guarantees a failed
// ImageList does NOT propagate as an error (best-effort cleanup) and
// does NOT trigger any ImageRemove calls.
func TestPruneStaleCPImages_ListFailure_Degrades(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)

	fake.FakeAPI.ImageListFn = func(_ context.Context, _ mobyclient.ImageListOptions) (mobyclient.ImageListResult, error) {
		return mobyclient.ImageListResult{}, fmt.Errorf("docker daemon went away")
	}
	var removed []string
	fake.FakeAPI.ImageRemoveFn = func(_ context.Context, ref string, _ mobyclient.ImageRemoveOptions) (mobyclient.ImageRemoveResult, error) {
		removed = append(removed, ref)
		return mobyclient.ImageRemoveResult{}, nil
	}

	// Function returns void — the test is that it doesn't panic and
	// doesn't try to remove anything when the list failed.
	pruneStaleCPImages(t.Context(), fake.Client, "ignored", logger.Nop())
	assert.Empty(t, removed, "list failure must short-circuit before any remove call")
}

// TestCPImageCreatedAt covers the LABEL → Docker Created fallback chain
// directly so each branch is pinned independently of recoverFromNameConflict.
func TestCPImageCreatedAt(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	const ref = "clawker-controlplane:bin-deadbeef00000000"

	// inspectWith returns a managed (passes whail's label-jail) image
	// inspect with explicit Created field and arbitrary extra labels.
	inspectWith := func(created string, extraLabels map[string]string) mobyclient.ImageInspectResult {
		labels := map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()}
		for k, v := range extraLabels {
			labels[k] = v
		}
		return mobyclient.ImageInspectResult{
			InspectResponse: dockerimage.InspectResponse{
				Created: created,
				Config: &dockerspec.DockerOCIImageConfig{
					ImageConfig: ocispec.ImageConfig{Labels: labels},
				},
			},
		}
	}

	t.Run("label parseable wins", func(t *testing.T) {
		fake := dockermocks.NewFakeClient(cfg)
		want := "2026-05-21T10:00:00Z"
		fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
			// Newer Created field — must be ignored when LABEL is parseable.
			return inspectWith("2026-05-21T11:00:00.123456789Z", map[string]string{consts.LabelImageCreated: want}), nil
		}
		got, err := cpImageCreatedAt(t.Context(), fake.Client, ref, logger.Nop())
		require.NoError(t, err)
		assert.Equal(t, want, got.UTC().Format(time.RFC3339), "LABEL must take precedence over Created field")
	})

	t.Run("label missing falls back to Created", func(t *testing.T) {
		fake := dockermocks.NewFakeClient(cfg)
		want := "2026-05-21T10:00:00.123456789Z"
		fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
			return inspectWith(want, nil), nil
		}
		got, err := cpImageCreatedAt(t.Context(), fake.Client, ref, logger.Nop())
		require.NoError(t, err)
		assert.Equal(t, want, got.UTC().Format(time.RFC3339Nano))
	})

	t.Run("malformed label falls back silently and logs warn", func(t *testing.T) {
		fake := dockermocks.NewFakeClient(cfg)
		validCreated := "2026-05-21T10:00:00.123456789Z"
		fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
			return inspectWith(validCreated, map[string]string{consts.LabelImageCreated: "not-a-timestamp"}), nil
		}
		got, err := cpImageCreatedAt(t.Context(), fake.Client, ref, logger.Nop())
		require.NoError(t, err, "malformed LABEL must fall through to Created field, not fail")
		assert.Equal(t, validCreated, got.UTC().Format(time.RFC3339Nano))
	})

	t.Run("both empty returns error", func(t *testing.T) {
		fake := dockermocks.NewFakeClient(cfg)
		fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
			return inspectWith("", nil), nil
		}
		_, err := cpImageCreatedAt(t.Context(), fake.Client, ref, logger.Nop())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no parseable created timestamp")
	})

	t.Run("inspect error propagates", func(t *testing.T) {
		fake := dockermocks.NewFakeClient(cfg)
		fake.FakeAPI.ImageInspectFn = func(_ context.Context, _ string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
			return mobyclient.ImageInspectResult{}, cerrdefs.ErrNotFound
		}
		_, err := cpImageCreatedAt(t.Context(), fake.Client, ref, logger.Nop())
		require.Error(t, err)
		assert.ErrorIs(t, err, cerrdefs.ErrNotFound)
	})
}

// TestEnsureRunning_NameConflict_UnmanagedSquat covers the case where
// Docker says the CP name is taken but the managed-label jail returns
// nothing — an operator-managed (or stale, unlabeled) container is
// squatting on the CP name. Must surface a typed error without
// touching the squatter.
func TestEnsureRunning_NameConflict_UnmanagedSquat(t *testing.T) {
	f := newBootstrapFixture(t)

	// All ContainerList calls return empty (managed-jail filter rejects
	// the squatter), but ContainerCreate returns Conflict so recovery
	// runs and observes the empty list.
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in use by an unmanaged container",
		"unmanaged squatter must surface as a typed message, not be silently adopted")
	assert.Equal(t, int32(1), f.calls.create.Load(), "no retry — squatter requires operator intervention")
	assert.Zero(t, f.calls.remove.Load(), "must NOT force-remove an unmanaged container")
}

// TestEnsureRunning_NameConflict_OurImageVanished_Retries covers the
// race where our just-built CP image is force-removed (concurrent
// `docker image rm`, prune, storage GC) between ensureCPImage and the
// recovery inspect. Recovery must signal retry rather than abort.
func TestEnsureRunning_NameConflict_OurImageVanished_Retries(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-peer1234567890ab"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-from-other-build",
	}

	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		// First list (pre-create): empty so create fires.
		// Recovery list (after conflict): returns peer.
		// Retry list (after errCPRecoveryRetry): empty so create succeeds.
		if listCalls.Add(1) == 2 {
			return mobyclient.ContainerListResult{Items: []container.Summary{{
				ID:     "peer-cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: peerLabels,
			}}}, nil
		}
		return mobyclient.ContainerListResult{}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		n := f.calls.create.Add(1)
		if n == 1 {
			return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
		}
		return mobyclient.ContainerCreateResult{ID: "fresh-cp-id"}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Running: true, Status: container.StateRunning},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		if ref == ourTag {
			return mobyclient.ImageInspectResult{}, cerrdefs.ErrNotFound
		}
		return managedImageInspect(f.cfg, "2026-05-21T10:00:00Z"), nil
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.NoError(t, err)
	assert.Equal(t, int32(2), f.calls.create.Load(),
		"vanished image must trigger retry, not abort: first create conflicts, second succeeds")
	assert.Equal(t, int32(2), f.calls.image.Load(),
		"retry must re-invoke ensureCPImageFn so a vanished image is rebuilt before the next ContainerCreate")
}

// TestEnsureRunning_NameConflict_ReensureImageFails covers the failure
// branch on the retry path: if ensureCPImageFn errors when re-resolving
// the image after recovery signals retry, createCPContainer must
// surface the error rather than spin into a doomed ContainerCreate.
func TestEnsureRunning_NameConflict_ReensureImageFails(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-peerXXXX00000000"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-mismatched",
	}

	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCalls.Add(1) == 2 {
			return mobyclient.ContainerListResult{Items: []container.Summary{{
				ID:     "peer-cp-id",
				Names:  []string{"/" + consts.ContainerCP},
				State:  container.StateRunning,
				Labels: peerLabels,
			}}}, nil
		}
		return mobyclient.ContainerListResult{}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Running: true, Status: container.StateRunning},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		if ref == ourTag {
			return mobyclient.ImageInspectResult{}, cerrdefs.ErrNotFound
		}
		return managedImageInspect(f.cfg, "2026-05-21T10:00:00Z"), nil
	}

	// First ensureCPImageFn call (from EnsureRunning) succeeds; second
	// call (from the retry path) fails — caller must surface the wrapped
	// error rather than retry into a doomed ContainerCreate.
	origEnsure := ensureCPImageFn
	t.Cleanup(func() { ensureCPImageFn = origEnsure })
	var ensureCalls atomic.Int32
	ensureCPImageFn = func(_ context.Context, _ *docker.Client, _ *logger.Logger) (string, error) {
		if ensureCalls.Add(1) == 1 {
			return cpImageRef(), nil
		}
		return "", fmt.Errorf("simulated build failure")
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-ensuring cp image before retry",
		"reensure failure on retry must surface as a wrapped error")
	assert.Equal(t, int32(1), f.calls.create.Load(),
		"retry must NOT fire a second ContainerCreate after reensure failed")
}

// TestEnsureRunning_NameConflict_PeerImageInspectFails covers the
// non-NotFound failure path on the peer's image inspect — recovery
// must propagate the wrapped error rather than picking a winner.
func TestEnsureRunning_NameConflict_PeerImageInspectFails(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-peerxxxx00000000"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-mismatched",
	}

	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		if listCalls.Add(1) == 1 {
			return mobyclient.ContainerListResult{}, nil
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{{
			ID:     "peer-cp-id",
			Names:  []string{"/" + consts.ContainerCP},
			State:  container.StateRunning,
			Labels: peerLabels,
		}}}, nil
	}
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Running: true, Status: container.StateRunning},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		switch ref {
		case ourTag:
			return managedImageInspect(f.cfg, "2026-05-21T10:00:00Z"), nil
		case peerImageRef:
			return mobyclient.ImageInspectResult{}, fmt.Errorf("storage backend unreachable")
		}
		return mobyclient.ImageInspectResult{}, fmt.Errorf("unexpected ref %q", ref)
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting recovered cp image",
		"peer-image inspect failure must propagate as a wrapped error, not be silently adopted")
	assert.Zero(t, f.calls.remove.Load(), "must not force-remove peer when timestamp comparison is uncertain")
}

// TestCreateCPContainer_MaxAttemptsExhausted covers the safety-net path
// where ContainerCreate keeps conflicting and recovery keeps signaling
// retry. The bounded loop (maxCreateAttempts) must give up with a
// typed error rather than spin.
func TestCreateCPContainer_MaxAttemptsExhausted(t *testing.T) {
	f := newBootstrapFixture(t)

	const peerImageRef = "clawker-controlplane:bin-loopypeer00000"
	peerLabels := map[string]string{
		f.cfg.LabelManaged():    f.cfg.ManagedLabelValue(),
		consts.LabelCPBinarySHA: "peer-binary-sha-always-older",
	}

	var listCalls atomic.Int32
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		// Every list past the very first one returns the peer so each
		// recovery cycle finds something to compare against.
		if listCalls.Add(1) == 1 {
			return mobyclient.ContainerListResult{}, nil
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{{
			ID:     "peer-cp-id",
			Names:  []string{"/" + consts.ContainerCP},
			State:  container.StateExited,
			Labels: peerLabels,
		}}}, nil
	}
	// Every create attempt returns Conflict — the loop must hit
	// maxCreateAttempts and surface the exhaustion error.
	f.fake.FakeAPI.ContainerCreateFn = func(_ context.Context, _ mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error) {
		f.calls.create.Add(1)
		return mobyclient.ContainerCreateResult{}, fmt.Errorf(`name "/clawker-controlplane" in use: %w`, cerrdefs.ErrConflict)
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:     id,
				Image:  peerImageRef,
				State:  &container.State{Status: container.StateExited},
				Config: &container.Config{Labels: peerLabels},
			},
		}, nil
	}
	// Our image is always newer → recovery always picks "replace peer"
	// → stop+remove peer, signal retry → next create conflicts again.
	const oursCreated = "2026-05-21T10:05:00Z"
	const theirsCreated = "2026-05-21T10:00:00Z"
	ourTag := cpImageRef()
	f.fake.FakeAPI.ImageInspectFn = func(_ context.Context, ref string, _ ...mobyclient.ImageInspectOption) (mobyclient.ImageInspectResult, error) {
		switch ref {
		case ourTag:
			return managedImageInspect(f.cfg, oursCreated), nil
		case peerImageRef:
			return managedImageInspect(f.cfg, theirsCreated), nil
		}
		return mobyclient.ImageInspectResult{}, fmt.Errorf("unexpected ref %q", ref)
	}

	err := EnsureRunning(t.Context(), f.ensureOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded 2 attempts",
		"persistent create conflicts must bottom out at the maxCreateAttempts safety net")
	assert.Equal(t, int32(2), f.calls.create.Load(),
		"loop must respect maxCreateAttempts=2, not retry forever")
}
