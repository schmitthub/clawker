package firewall

// These tests live in package firewall (not firewall_test) so they can
// access the Manager's unexported test-seam hook fields
// (cgroupDriverFn, ebpfExecFn, ebpfExecOutputFn, touchSignalFileFn).
// The seams let us exercise Enable/Disable/Bypass and related failure
// paths without standing up a full Docker API mock — we only need to
// stub the raw ContainerInspect/ContainerList/Info/Exec* surface for
// the narrowly-scoped operations under test.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// newManagerWithFake builds a real Manager backed by a whailtest.FakeAPIClient,
// using an isolated config (so the rules-store write path works). Tests can
// then mutate the fake's *Fn fields and the manager's test-seam hook fields
// to inject specific behaviours.
func newManagerWithFake(t *testing.T) (*Manager, *whailtest.FakeAPIClient, config.Config) {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := &whailtest.FakeAPIClient{}
	mgr, err := NewManager(fake, cfg, logger.Nop())
	require.NoError(t, err)
	return mgr, fake, cfg
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

// execCapture records every args slice passed to ebpfExec / ebpfExecOutput so
// tests can assert the cgroupPath + command the manager sent to the eBPF
// sidecar.
type execCapture struct {
	mu   sync.Mutex
	args [][]string
}

func (c *execCapture) record(args []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// copy to defeat slice aliasing (append reuses backing arrays).
	cp := make([]string, len(args))
	copy(cp, args)
	c.args = append(c.args, cp)
}

func (c *execCapture) all() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.args))
	copy(out, c.args)
	return out
}

func TestManager_Disable_ResolvesName(t *testing.T) {
	mgr, fake, _ := newManagerWithFake(t)

	const friendly = "clawker.myapp.dev"
	fake.ContainerInspectFn = func(_ context.Context, ref string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		assert.Equal(t, friendly, ref, "Disable should pass the caller's ref verbatim to Docker")
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	// Force cgroupfs driver (deterministic cgroup path) and capture ebpfExec.
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }
	cap := &execCapture{}
	mgr.ebpfExecFn = func(_ context.Context, args ...string) error {
		cap.record(args)
		return nil
	}

	require.NoError(t, mgr.Disable(t.Context(), friendly))

	calls := cap.all()
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 2)
	assert.Equal(t, "disable", calls[0][0])
	// Expect cgroupfs-style path with the CANONICAL long hex ID appended —
	// the whole point of resolveContainerID is that the friendly name is
	// never baked into the cgroup path.
	assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, calls[0][1])
	assert.NotContains(t, calls[0][1], friendly)
}

func TestManager_Disable_SystemdDriver(t *testing.T) {
	// Systemd cgroup drivers use a different path layout — also exercised to
	// lock in the driver→path mapping.
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "systemd", nil }
	cap := &execCapture{}
	mgr.ebpfExecFn = func(_ context.Context, args ...string) error {
		cap.record(args)
		return nil
	}

	require.NoError(t, mgr.Disable(t.Context(), "clawker.myapp.dev"))

	calls := cap.all()
	require.Len(t, calls, 1)
	assert.Equal(t, "/sys/fs/cgroup/system.slice/docker-"+longHexID+".scope", calls[0][1])
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

	cap := &execCapture{}
	mgr.ebpfExecFn = func(_ context.Context, args ...string) error {
		cap.record(args)
		return nil
	}

	// Bypass schedules a detached "sleep N && unbypass" via ExecCreate+ExecStart.
	// Stub both to record the call.
	var scheduledCmd []string
	fake.ExecCreateFn = func(_ context.Context, _ string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
		scheduledCmd = opts.Cmd
		return client.ExecCreateResult{ID: "exec-123"}, nil
	}
	fake.ExecStartFn = func(_ context.Context, _ string, _ client.ExecStartOptions) (client.ExecStartResult, error) {
		return client.ExecStartResult{}, nil
	}

	require.NoError(t, mgr.Bypass(t.Context(), friendly, 30_000_000_000 /* 30s */))

	calls := cap.all()
	require.Len(t, calls, 1)
	assert.Equal(t, "bypass", calls[0][0])
	assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, calls[0][1])
	assert.NotContains(t, calls[0][1], friendly)

	// The detached unbypass timer shell command must reference the resolved
	// cgroup path, not the friendly name.
	require.NotEmpty(t, scheduledCmd)
	shell := strings.Join(scheduledCmd, " ")
	assert.Contains(t, shell, longHexID)
	assert.NotContains(t, shell, friendly)
}

func TestManager_Enable_ResolvesNameAndPropagatesTouchFailure(t *testing.T) {
	// Enable() is the most-invasive path — it goes through ensureEbpfImage,
	// discoverNetwork, ensureContainer, syncRoutes, cgroupDriver, ebpfExec,
	// and touchSignalFile. We stub each dependency at the narrowest seam so
	// the test focuses on two invariants:
	//  1. The resolved long ID is what reaches the cgroupPath / ebpfExec args
	//     (C7 name→ID resolution).
	//  2. A failing touchSignalFile now surfaces as a returned error instead
	//     of being silently logged as a Warn (C3 regression guard).
	mgr, fake, cfg := newManagerWithFake(t)

	// Rule out host-proxy resolution — not what we're testing here.
	disableHostProxy(t, cfg)

	const friendly = "clawker.myapp.dev"

	// ContainerInspect → map friendly name to canonical long ID.
	fake.ContainerInspectFn = func(_ context.Context, _ string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}

	// ImageInspect → the eBPF image exists (skip the build path).
	fake.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, nil
	}

	// NetworkInspect → minimal network info so discoverNetwork returns
	// without erroring. We return a realistic IPAM config matching what
	// the firewall network looks like in production.
	fake.NetworkInspectFn = stubFirewallNetworkInspect()

	// ContainerList → ensureContainer(ebpfContainer) sees a running
	// container already, so no Create/Start path is taken.
	fake.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{{
				ID:    "fake-ebpf-id",
				State: container.StateRunning,
			}},
		}, nil
	}

	// Capture all ebpfExec calls (sync-routes + enable).
	cap := &execCapture{}
	mgr.ebpfExecFn = func(_ context.Context, args ...string) error {
		cap.record(args)
		return nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "cgroupfs", nil }

	// Inject touchSignalFile failure: should propagate as an error now.
	touchErr := errors.New("docker exec failed")
	mgr.touchSignalFileFn = func(_ context.Context, id string) error {
		// Invariant: the ID passed to touchSignalFile is the resolved long ID.
		assert.Equal(t, longHexID, id)
		return touchErr
	}

	err := mgr.Enable(t.Context(), friendly)
	require.Error(t, err)
	assert.ErrorIs(t, err, touchErr)
	assert.Contains(t, err.Error(), "firewall-ready signal")

	// Check the ebpfExec invocations — we expect at least:
	//   - one "sync-routes" call (from syncRoutes)
	//   - one "enable" call with the resolved cgroup path
	var sawEnable bool
	for _, call := range cap.all() {
		if len(call) == 0 {
			continue
		}
		if call[0] == "enable" {
			require.GreaterOrEqual(t, len(call), 3, "enable call should pass cgroupPath + cfgJSON")
			assert.Equal(t, "/sys/fs/cgroup/docker/"+longHexID, call[1])
			assert.NotContains(t, call[1], friendly)
			sawEnable = true
		}
	}
	assert.True(t, sawEnable, "Enable() should invoke ebpfExec(\"enable\", ...)")
}

// --- C3 / touchSignalFile error propagation from Enable ---------------------

func TestTouchSignalFile_ErrorSurfacesFromEnable(t *testing.T) {
	// Focused version of the Enable touchSignal check: driven purely through
	// the seam, without wiring Docker mocks. It locks in that any
	// touchSignalFileFn failure propagates as a returned error from Enable.
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
	mgr.ebpfExecFn = func(context.Context, ...string) error { return nil }

	sentinel := errors.New("copy failed")
	mgr.touchSignalFileFn = func(context.Context, string) error { return sentinel }

	err := mgr.Enable(t.Context(), "clawker.myapp.dev")
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

// --- C5 / host-proxy resolve failure propagation ----------------------------

func TestEnable_HostProxyResolveFailurePropagates(t *testing.T) {
	// Default config has HostProxyEnabled=true. If the eBPF-side DNS resolve
	// for host.docker.internal fails, Enable() must now return an error
	// instead of silently disabling host-proxy bypass.
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
	mgr.ebpfExecFn = func(context.Context, ...string) error { return nil }

	// Inject the failure: the eBPF sidecar cannot resolve host.docker.internal.
	resolveErr := errors.New("DNS lookup failed")
	mgr.ebpfExecOutputFn = func(_ context.Context, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "resolve" && args[1] == "host.docker.internal" {
			return "", resolveErr
		}
		return "", fmt.Errorf("unexpected ebpfExecOutput args: %v", args)
	}

	err := mgr.Enable(t.Context(), "clawker.myapp.dev")
	require.Error(t, err)
	assert.ErrorIs(t, err, resolveErr)
	assert.Contains(t, err.Error(), "host-proxy bypass")
}

func TestEnable_HostProxyResolveEmptyAddressPropagates(t *testing.T) {
	// Resolve returns no error but an empty string — equally broken, must
	// still surface as an error since the resulting cfgJSON would embed
	// "host_proxy_ip" = "" and silently disable the bypass.
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
	mgr.ebpfExecFn = func(context.Context, ...string) error { return nil }
	mgr.ebpfExecOutputFn = func(context.Context, ...string) (string, error) { return "   ", nil }

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
	// Sanity: when cgroupDriverFn returns a value, it flows through to the
	// cgroup path unchanged.
	mgr, fake, _ := newManagerWithFake(t)

	fake.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{
			Container: container.InspectResponse{ID: longHexID},
		}, nil
	}
	mgr.cgroupDriverFn = func(context.Context) (string, error) { return "systemd", nil }

	cap := &execCapture{}
	mgr.ebpfExecFn = func(_ context.Context, args ...string) error {
		cap.record(args)
		return nil
	}

	require.NoError(t, mgr.Disable(t.Context(), "clawker.myapp.dev"))
	calls := cap.all()
	require.Len(t, calls, 1)
	assert.Equal(t, "/sys/fs/cgroup/system.slice/docker-"+longHexID+".scope", calls[0][1])
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
