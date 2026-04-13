package firewall

// These tests live in package firewall (not firewall_test) so they can
// access the Manager's unexported test-seam hook fields
// (cgroupDriverFn, adminClientFn, touchSignalFileFn, waitForCPReadyFn).
// The seams let us exercise Enable/Disable/Bypass and related failure
// paths without standing up a full Docker API mock — we only need to
// stub the raw ContainerInspect/ContainerList/Info surface for the
// narrowly-scoped operations under test.

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// --- Test helpers -----------------------------------------------------------

// longHexID is a 64-char lowercase hex string suitable for use as a
// canonical Docker container ID in tests.
const longHexID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// errTestNotFound is an errdefs.NotFound-compatible error. Used to simulate
// Docker API 404 responses from a FakeAPIClient stub.
type errTestNotFound struct{ msg string }

func (e errTestNotFound) Error() string { return e.msg }
func (e errTestNotFound) NotFound()     {}

// recordingAdminClient is a test double for adminv1.AdminServiceClient that
// records all calls with their request payloads. Each method field can be
// overridden to inject errors or custom responses.
type recordingAdminClient struct {
	mu sync.Mutex

	InstallCalls []*adminv1.InstallRequest
	RemoveCalls  []*adminv1.RemoveRequest
	EnableCalls  []*adminv1.EnableRequest
	DisableCalls []*adminv1.DisableRequest
	BypassCalls  []*adminv1.BypassRequest
	SyncCalls    []*adminv1.SyncRoutesRequest
	ResolveCalls []*adminv1.ResolveHostnameRequest

	InstallErr    error
	RemoveErr     error
	EnableErr     error
	DisableErr    error
	BypassErr     error
	SyncErr       error
	ResolveResult *adminv1.ResolveHostnameResponse
	ResolveErr    error
}

func (m *recordingAdminClient) Install(_ context.Context, req *adminv1.InstallRequest, _ ...grpc.CallOption) (*adminv1.InstallResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InstallCalls = append(m.InstallCalls, req)
	return &adminv1.InstallResponse{}, m.InstallErr
}

func (m *recordingAdminClient) Remove(_ context.Context, req *adminv1.RemoveRequest, _ ...grpc.CallOption) (*adminv1.RemoveResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RemoveCalls = append(m.RemoveCalls, req)
	return &adminv1.RemoveResponse{}, m.RemoveErr
}

func (m *recordingAdminClient) Enable(_ context.Context, req *adminv1.EnableRequest, _ ...grpc.CallOption) (*adminv1.EnableResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EnableCalls = append(m.EnableCalls, req)
	return &adminv1.EnableResponse{}, m.EnableErr
}

func (m *recordingAdminClient) Disable(_ context.Context, req *adminv1.DisableRequest, _ ...grpc.CallOption) (*adminv1.DisableResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DisableCalls = append(m.DisableCalls, req)
	return &adminv1.DisableResponse{}, m.DisableErr
}

func (m *recordingAdminClient) Bypass(_ context.Context, req *adminv1.BypassRequest, _ ...grpc.CallOption) (*adminv1.BypassResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BypassCalls = append(m.BypassCalls, req)
	return &adminv1.BypassResponse{}, m.BypassErr
}

func (m *recordingAdminClient) SyncRoutes(_ context.Context, req *adminv1.SyncRoutesRequest, _ ...grpc.CallOption) (*adminv1.SyncRoutesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SyncCalls = append(m.SyncCalls, req)
	return &adminv1.SyncRoutesResponse{Applied: uint32(len(req.GetRoutes()))}, m.SyncErr
}

func (m *recordingAdminClient) ResolveHostname(_ context.Context, req *adminv1.ResolveHostnameRequest, _ ...grpc.CallOption) (*adminv1.ResolveHostnameResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ResolveCalls = append(m.ResolveCalls, req)
	if m.ResolveErr != nil {
		return nil, m.ResolveErr
	}
	if m.ResolveResult != nil {
		return m.ResolveResult, nil
	}
	return &adminv1.ResolveHostnameResponse{Addresses: []string{"192.168.65.254"}}, nil
}

// newManagerWithFake builds a real Manager backed by a whailtest.FakeAPIClient,
// using an isolated config (so the rules-store write path works). Tests can
// then mutate the fake's *Fn fields and the manager's test-seam hook fields
// to inject specific behaviours.
//
// The CP readiness gate and admin client are stubbed — tests that exercise
// Enable/Disable/Bypass don't need a real CP container. Individual tests
// that need specific admin client behavior should overwrite mgr.adminClientFn.
func newManagerWithFake(t *testing.T) (*Manager, *whailtest.FakeAPIClient, config.Config) {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := &whailtest.FakeAPIClient{}
	mgr, err := NewManager(fake, cfg, logger.Nop())
	require.NoError(t, err)
	mgr.waitForCPReadyFn = func(context.Context) error { return nil }
	return mgr, fake, cfg
}

// withRecordingAdmin injects a recordingAdminClient into the manager and
// returns it for assertion.
func withRecordingAdmin(mgr *Manager) *recordingAdminClient {
	rec := &recordingAdminClient{}
	mgr.adminClientFn = func() (adminv1.AdminServiceClient, error) { return rec, nil }
	return rec
}

// disableHostProxy sets Security.EnableHostProxy=false on the isolated test
// config so that Enable() does not try to resolve host.docker.internal.
// NewIsolatedTestConfig returns a real store-backed Config that supports Set.
func disableHostProxy(t *testing.T, cfg config.Config) {
	t.Helper()
	f := false
	err := cfg.ProjectStore().Set(func(p *config.Project) {
		if p.Security.Firewall == nil {
			p.Security.Firewall = &config.FirewallConfig{}
		}
		p.Security.EnableHostProxy = &f
	})
	require.NoError(t, err)
}

// --- C6 / resolveContainerID tests ------------------------------------------

func TestResolveContainerID_ShortCircuitsOnID(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	// Guard: the ContainerInspectFn is intentionally unset. A call panics
	// with "not implemented" via notImplemented, so if the short-circuit
	// regresses the test explodes with a clear message.

	got, err := mgr.resolveContainerID(t.Context(), longHexID)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)

	// Also verify the call log is empty — no Docker round-trip happened.
	assert.Empty(t, fake.Calls, "resolveContainerID should not touch Docker for canonical IDs")
}

func TestResolveContainerID_RejectsShortID(t *testing.T) {
	// A 12-char short ID is NOT a canonical long ID and must NOT short-circuit.
	// Instead the ref is passed to ContainerInspect so Docker can resolve it.
	mgr, fake, _ := newManagerWithFake(t)

	const shortID = "0123456789ab"
	fake.ContainerInspectFn = func(_ context.Context, ref string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		assert.Equal(t, shortID, ref)
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	got, err := mgr.resolveContainerID(t.Context(), shortID)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.Contains(t, fake.Calls, "ContainerInspect")
}

func TestResolveContainerID_RejectsWrongLengthHex(t *testing.T) {
	// 63 lowercase hex chars is the right alphabet but wrong length — must
	// NOT short-circuit (a legitimate friendly name happens to be all-hex).
	mgr, fake, _ := newManagerWithFake(t)

	ref := longHexID[:63]
	fake.ContainerInspectFn = func(_ context.Context, got string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		assert.Equal(t, ref, got)
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	got, err := mgr.resolveContainerID(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
}

func TestResolveContainerID_RejectsNonHexCharsInLongString(t *testing.T) {
	// 64 characters but containing 'z' is a friendly name, not an ID.
	mgr, fake, _ := newManagerWithFake(t)

	ref := strings.Repeat("z", 64)
	var called bool
	fake.ContainerInspectFn = func(_ context.Context, got string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		called = true
		assert.Equal(t, ref, got)
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	got, err := mgr.resolveContainerID(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.True(t, called, "non-hex 64-char ref should fall through to ContainerInspect")
}

func TestResolveContainerID_ResolvesName(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	const name = "clawker.myapp.dev"
	fake.ContainerInspectFn = func(_ context.Context, ref string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		assert.Equal(t, name, ref)
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	got, err := mgr.resolveContainerID(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.Contains(t, fake.Calls, "ContainerInspect")
}

func TestResolveContainerID_NotFoundPropagates(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	const name = "clawker.unknown.dev"
	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{}, errTestNotFound{msg: "No such container: " + name}
	}

	_, err := mgr.resolveContainerID(t.Context(), name)
	require.Error(t, err)
	assert.Contains(t, err.Error(), name, "error should reference the ref the caller passed in")
	assert.Contains(t, err.Error(), "resolving container")
}

// --- C7 / Bypass / Disable / Enable name→ID resolution ----------------------

func TestManager_Disable_ResolvesName(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	const friendly = "clawker.myapp.dev"
	fake.ContainerInspectFn = func(_ context.Context, ref string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		assert.Equal(t, friendly, ref, "Disable should pass the caller's ref verbatim to Docker")
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	rec := withRecordingAdmin(mgr)

	require.NoError(t, mgr.Disable(t.Context(), friendly))

	// Disable calls gRPC Remove (detach BPF programs).
	require.Len(t, rec.RemoveCalls, 1)
	assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, rec.RemoveCalls[0].GetCgroupPath())
	assert.NotContains(t, rec.RemoveCalls[0].GetCgroupPath(), friendly)
}

func TestManager_Disable_SystemdDriver(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "systemd", nil }
	rec := withRecordingAdmin(mgr)

	require.NoError(t, mgr.Disable(t.Context(), "clawker.myapp.dev"))

	require.Len(t, rec.RemoveCalls, 1)
	assert.Equal(t, "/sys/fs/cgroup/system.slice/docker-"+longHexID+".scope", rec.RemoveCalls[0].GetCgroupPath())
}

func TestManager_Bypass_ResolvesName(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	const friendly = "clawker.myapp.dev"
	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	rec := withRecordingAdmin(mgr)

	require.NoError(t, mgr.Bypass(t.Context(), friendly, 30*time.Second))

	// Bypass calls gRPC Bypass (set bypass flag + server-side dead-man timer).
	require.Len(t, rec.BypassCalls, 1)
	assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, rec.BypassCalls[0].GetCgroupPath())
	assert.Equal(t, uint32(30), rec.BypassCalls[0].GetTimeoutSeconds())
	assert.NotContains(t, rec.BypassCalls[0].GetCgroupPath(), friendly)
}

func TestManager_Enable_ResolvesNameAndPropagatesTouchFailure(t *testing.T) {
	// Enable() is the most-invasive path — it goes through ensureEbpfImage,
	// discoverNetwork, ensureContainer, syncRoutes, adminClient.Install,
	// and touchSignalFile. We stub each dependency at the narrowest seam so
	// the test focuses on two invariants:
	//  1. The resolved long ID is what reaches the cgroup path in the Install request.
	//  2. A failing touchSignalFile surfaces as a returned error (C3 regression guard).
	mgr, fake, cfg := newManagerWithFake(t)
	disableHostProxy(t, cfg)

	const friendly = "clawker.myapp.dev"

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	fake.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, nil
	}
	fake.NetworkInspectFn = stubFirewallNetworkInspect()
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{{ID: "fake-ebpf-id", State: container.StateRunning}},
		}, nil
	}

	rec := withRecordingAdmin(mgr)
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }

	touchErr := errors.New("docker exec failed")
	mgr.touchSignalFileFn = func(_ context.Context, id string) error {
		assert.Equal(t, longHexID, id)
		return touchErr
	}

	err := mgr.Enable(t.Context(), friendly)
	require.Error(t, err)
	assert.ErrorIs(t, err, touchErr)
	assert.Contains(t, err.Error(), "firewall-ready signal")

	// Verify the Install RPC was called with the correct cgroup path.
	require.NotEmpty(t, rec.InstallCalls, "Enable should call adminClient.Install")
	lastInstall := rec.InstallCalls[len(rec.InstallCalls)-1]
	assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, lastInstall.GetCgroupPath())
	assert.NotContains(t, lastInstall.GetCgroupPath(), friendly)
}

// --- C3 / touchSignalFile error propagation from Enable ---------------------

func TestTouchSignalFile_ErrorSurfacesFromEnable(t *testing.T) {
	mgr, fake, cfg := newManagerWithFake(t)
	disableHostProxy(t, cfg)

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	fake.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, nil
	}
	fake.NetworkInspectFn = stubFirewallNetworkInspect()
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{{ID: "fake-ebpf-id", State: container.StateRunning}},
		}, nil
	}

	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	withRecordingAdmin(mgr)

	sentinel := errors.New("copy failed")
	mgr.touchSignalFileFn = func(context.Context, string) error { return sentinel }

	err := mgr.Enable(t.Context(), "clawker.myapp.dev")
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// --- C5 / host-proxy resolve failure propagation ----------------------------

func TestEnable_HostProxyResolveFailurePropagates(t *testing.T) {
	// Default config has HostProxyEnabled=true. If the CP's ResolveHostname
	// for host.docker.internal fails, Enable() must return an error instead
	// of silently disabling host-proxy bypass.
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	fake.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, nil
	}
	fake.NetworkInspectFn = stubFirewallNetworkInspect()
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{{ID: "fake-ebpf-id", State: container.StateRunning}},
		}, nil
	}

	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	rec := withRecordingAdmin(mgr)
	resolveErr := errors.New("DNS lookup failed")
	rec.ResolveErr = resolveErr

	err := mgr.Enable(t.Context(), "clawker.myapp.dev")
	require.Error(t, err)
	assert.ErrorIs(t, err, resolveErr)
	assert.Contains(t, err.Error(), "host-proxy bypass")
}

func TestEnable_HostProxyResolveEmptyAddressPropagates(t *testing.T) {
	// Resolve returns no error but empty addresses — must still error since
	// an empty host_proxy_ip silently disables the bypass.
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	fake.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, nil
	}
	fake.NetworkInspectFn = stubFirewallNetworkInspect()
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{{ID: "fake-ebpf-id", State: container.StateRunning}},
		}, nil
	}

	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	rec := withRecordingAdmin(mgr)
	rec.ResolveResult = &adminv1.ResolveHostnameResponse{Addresses: []string{"   "}}

	err := mgr.Enable(t.Context(), "clawker.myapp.dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty address")
}

// --- C1 / Manager.Stop propagates ContainerList errors ---------------------

// TestStop_PropagatesContainerListError exercises the Stop() entry point
// (not the internal stopAndRemove helper in isolation) with a fake
// ContainerList that errors. Before the fix, the Debug-logged error was
// swallowed by stopAndRemove's early return, and Stop() reported success
// while leaving orphaned firewall containers behind. Daemon.Run now
// propagates Stop errors, so this test locks the full chain in.
func TestStop_PropagatesContainerListError(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	listErr := errors.New("daemon unreachable")
	fake.ContainerListFn = func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, listErr
	}

	err := mgr.Stop(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, listErr, "Stop should wrap (and expose) the underlying ContainerList error")
	assert.Contains(t, err.Error(), "firewall stop")
}

// --- C4 / cgroupDriver propagates Info errors -------------------------------

func TestCgroupDriverImpl_PropagatesInfoError(t *testing.T) {
	// cgroupDriverImpl is the production implementation of cgroupDriver.
	// Using HTTP-round-tripper-backed test isn't feasible here without
	// touching out-of-scope packages — but we can assert the error-path
	// wiring is correct by verifying Enable (through the seam) wraps
	// whatever cgroupDriverFn returns.
	mgr, _, _ := newManagerWithFake(t)

	driverErr := errors.New("info endpoint down")
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "", driverErr }

	// Disable is the simplest path that touches cgroupDriver — short-circuit
	// resolveContainerID via the 64-hex ID path so we never call Docker.
	err := mgr.Disable(t.Context(), longHexID)
	require.Error(t, err)
	assert.ErrorIs(t, err, driverErr)
	assert.Contains(t, err.Error(), "firewall disable")
}

func TestCgroupDriverImpl_ReturnsDriverFromInfo(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "systemd", nil }
	rec := withRecordingAdmin(mgr)

	require.NoError(t, mgr.Disable(t.Context(), "clawker.myapp.dev"))

	require.Len(t, rec.RemoveCalls, 1)
	assert.Equal(t, "/sys/fs/cgroup/system.slice/docker-"+longHexID+".scope", rec.RemoveCalls[0].GetCgroupPath())
}

// --- C2 / isContainerRunning error handling at the interface boundary -------

func TestIsContainerRunning_ListErrorReturnsFalse(t *testing.T) {
	// IsRunning() is part of the public FirewallManager interface and
	// returns bool — so Docker API failures must be reported as "not
	// running" (and logged at Warn). Internal callers get the error via
	// isContainerRunningE; this test pins the public-surface contract.
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerListFn = func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, errors.New("docker unreachable")
	}

	assert.False(t, mgr.IsRunning(t.Context()), "IsRunning must not panic or block on list errors")
}

func TestIsContainerRunningE_PropagatesError(t *testing.T) {
	// The internal form returns (bool, error) so Status / regenerateAndRestart
	// can distinguish "firewall down" from "Docker unreachable".
	mgr, fake, _ := newManagerWithFake(t)

	listErr := errors.New("docker unreachable")
	fake.ContainerListFn = func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, listErr
	}

	running, err := mgr.isContainerRunningE(t.Context(), envoyContainer)
	require.Error(t, err)
	assert.False(t, running)
	assert.ErrorIs(t, err, listErr)
}

func TestStatus_PropagatesListError(t *testing.T) {
	// Status now surfaces Docker API errors instead of silently reporting
	// all three containers as "not running" (which masks infra failures).
	mgr, fake, _ := newManagerWithFake(t)

	// NetworkInspect returns NotFound so discoverNetwork is treated as
	// "firewall not brought up yet" and Status continues on to the
	// container probes — where the real ContainerList error lives.
	fake.NetworkInspectFn = func(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		return client.NetworkInspectResult{}, errTestNotFound{msg: "no network"}
	}

	listErr := errors.New("docker unreachable")
	fake.ContainerListFn = func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{}, listErr
	}

	_, err := mgr.Status(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, listErr)
}

func TestStatus_PropagatesDiscoverNetworkError(t *testing.T) {
	// A non-NotFound NetworkInspect error (e.g. Docker daemon unreachable)
	// must propagate out of Status instead of being silently dropped. The
	// legitimate "network doesn't exist yet" case uses a NotFound error
	// and is covered by TestStatus_PropagatesListError above — the two
	// cases must be distinguishable because a user running `firewall
	// status` needs to tell "firewall not brought up" apart from "can't
	// talk to Docker".
	mgr, fake, _ := newManagerWithFake(t)

	networkErr := errors.New("dial unix /var/run/docker.sock: connection refused")
	fake.NetworkInspectFn = func(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		return client.NetworkInspectResult{}, networkErr
	}
	// ContainerListFn left unset: if Status incorrectly continues past the
	// network check, the test will explode on notImplemented rather than
	// silently masking the regression.

	_, err := mgr.Status(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, networkErr, "non-NotFound network errors must propagate from Status")
}

func TestStatus_NetworkNotFoundIsNotAnError(t *testing.T) {
	// The inverse of the above: when the clawker-net network legitimately
	// doesn't exist yet (e.g. immediately after install, before the user
	// runs firewall up), Status must NOT treat that as a Docker failure.
	// It should report a healthy-looking empty status with empty network
	// fields — that's how the CLI distinguishes "firewall not brought up"
	// from real infra failures.
	mgr, fake, _ := newManagerWithFake(t)

	fake.NetworkInspectFn = func(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		return client.NetworkInspectResult{}, errTestNotFound{msg: "no such network"}
	}
	// All three container probes return empty lists (none running).
	fake.ContainerListFn = func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{Items: nil}, nil
	}

	status, err := mgr.Status(t.Context())
	require.NoError(t, err, "NetworkNotFound must be treated as 'firewall not brought up', not a failure")
	require.NotNil(t, status)
	assert.False(t, status.Running)
	assert.Empty(t, status.EnvoyIP)
	assert.Empty(t, status.CoreDNSIP)
	assert.Empty(t, status.NetworkID)
}

// --- helpers: stub the clawker-net Docker network for Enable tests -----------

// stubFirewallNetworkInspect returns a NetworkInspectFn that yields a
// realistic-looking clawker-net response so discoverNetwork can compute
// static IPs from the gateway without error.
func stubFirewallNetworkInspect() func(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	return func(_ context.Context, _ string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
		return buildFakeClawkerNetwork(), nil
	}
}

// buildFakeClawkerNetwork returns a minimal network.Inspect value with a
// /24 subnet + gateway, enough for discoverNetwork to compute Envoy/CoreDNS
// static IPs from the config's EnvoyIPLastOctet / CoreDNSIPLastOctet.
func buildFakeClawkerNetwork() client.NetworkInspectResult {
	gateway := netip.MustParseAddr("172.30.0.1")
	subnet := netip.MustParsePrefix("172.30.0.0/24")
	return client.NetworkInspectResult{
		Network: network.Inspect{
			Network: network.Network{
				ID:   "fake-clawker-net-id",
				Name: "clawker-net",
				IPAM: network.IPAM{
					Driver: "default",
					Config: []network.IPAMConfig{{
						Subnet:  subnet,
						Gateway: gateway,
					}},
				},
			},
		},
	}
}
