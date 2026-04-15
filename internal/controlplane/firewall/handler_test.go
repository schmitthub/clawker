package firewall

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testCgroupPath is the synthetic path the test resolver hands back for
// alive containers. The handler never stats it — the injected
// cgroupIDFn on newTestHandler returns testCgroupID unconditionally —
// so the path only needs to be stable for logging + drift bookkeeping.
// Real kernel paths would require Linux + a live cgroupfs, which is
// exactly what we want to avoid on dev boxes.
const (
	testCgroupPath = "/test/cgroup/path"
	testCgroupID   = uint64(42)
)

// testNetInfo returns the canonical topology the fakeStack hands back to
// FirewallEnable. Handler builds the BPF container_config from these
// fields + cfg — tests never populate that struct directly anymore.
func testNetInfo() *NetworkInfo {
	return &NetworkInfo{
		NetworkID: "net-test",
		Gateway:   netip.MustParseAddr("172.20.0.1"),
		EnvoyIP:   "172.20.0.2",
		CoreDNSIP: "172.20.0.3",
		CIDR:      "172.20.0.0/16",
	}
}

// nopResolver always reports the container alive at testCgroupPath. The
// canonical id in the response equals the input ref so callers can key
// state on the request value directly.
func nopResolver(_ context.Context, ref string) (string, string, bool, error) {
	return ref, testCgroupPath, true, nil
}

// fakeStack is the test double for StackLifecycle. Tracks the call
// counts the tests actually assert on (`ensureRunningCalls`,
// `reloadCalls`); other methods just satisfy the interface.
type fakeStack struct {
	mu                                           sync.Mutex
	ensureRunningCalls, reloadCalls, statusCalls int
	ensureErr                                    error
	statusResult                                 Status
	netInfo                                      *NetworkInfo
	netInfoErr                                   error
}

func (f *fakeStack) EnsureRunning(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureRunningCalls++
	return f.ensureErr
}

func (f *fakeStack) Stop(_ context.Context) error { return nil }

func (f *fakeStack) Reload(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloadCalls++
	return nil
}

func (f *fakeStack) Status(_ context.Context) (*Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	st := f.statusResult
	return &st, nil
}

func (f *fakeStack) NetworkInfo(_ context.Context) (*NetworkInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.netInfoErr != nil {
		return nil, f.netInfoErr
	}
	if f.netInfo != nil {
		return f.netInfo, nil
	}
	return testNetInfo(), nil
}

// testConfig returns a mock Config with defaults (HostProxy enabled,
// EnvoyEgressPort=10000). FirewallEnable reads host-proxy + egress port
// from cfg, so the handler must be wired with one even for ebpf-only
// tests. Blank is sufficient — we never mutate.
func testConfig() config.Config {
	return configmocks.NewBlankConfig()
}

// newTestHandler builds a Handler with the given EBPF mock and resolver,
// a fake stack, and no rule store. Tests that exercise rule-store paths
// build their own Handler with a real store via testenv. resolveHostFn
// is wired to a fixed answer so FirewallEnable's host-proxy bypass path
// (default-enabled in the mock config) doesn't hit the real resolver.
func newTestHandler(mock *ebpfmocks.EBPFManagerMock, resolver ContainerResolver) *Handler {
	if resolver == nil {
		resolver = nopResolver
	}
	h := NewHandler(HandlerDeps{
		EBPF:     mock,
		Stack:    &fakeStack{},
		Cfg:      testConfig(),
		Resolver: resolver,
		Log:      logger.Nop(),
	})
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return []string{"192.168.65.254"}, nil
	}
	h.cgroupIDFn = func(string) (uint64, error) { return testCgroupID, nil }
	return h
}

// noopMock returns an ebpf mock with all methods set to no-op success.
func noopMock() *ebpfmocks.EBPFManagerMock {
	return &ebpfmocks.EBPFManagerMock{
		InstallFunc: func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error {
			return nil
		},
		RemoveFunc:     func(_ uint64) error { return nil },
		EnableFunc:     func(_ uint64) error { return nil },
		DisableFunc:    func(_ uint64) error { return nil },
		SyncRoutesFunc: func(_ []ebpf.Route) error { return nil },
		FlushAllFunc:   func() error { return nil },
	}
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Errorf("status code = %v, want %v (message: %s)", st.Code(), want, st.Message())
	}
}

// ---------------------------------------------------------------------------
// FirewallEnable
// ---------------------------------------------------------------------------

func TestHandler_FirewallEnable_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)

	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-1",
	})
	if err != nil {
		t.Fatalf("FirewallEnable returned error: %v", err)
	}
	if got := len(mock.InstallCalls()); got != 1 {
		t.Fatalf("Install called %d times, want 1", got)
	}
	// Assert the BPF container_config was derived from stack+cfg, not
	// the request. testNetInfo seeds 172.20.0.0/16 with gateway .1;
	// testConfig() pins EnvoyEgressPort=10000 and the handler's
	// resolveHostFn pins hostProxyIp=192.168.65.254.
	got := mock.InstallCalls()[0].Cfg
	if got.EgressPort != 10000 {
		t.Errorf("EgressPort = %d, want 10000 (from cfg)", got.EgressPort)
	}
	if got.HostProxyPort == 0 {
		t.Errorf("HostProxyPort = 0, want cfg default (host proxy enabled by default)")
	}
}

func TestHandler_FirewallEnable_EmptyContainerID(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallEnable_ContainerGone_FailedPrecondition(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	h := newTestHandler(noopMock(), resolver)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-gone",
	})
	assertCode(t, err, codes.FailedPrecondition)
}

func TestHandler_FirewallEnable_NetworkInfoError_PropagatesInternal(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)
	h.stack.(*fakeStack).netInfoErr = errors.New("docker unreachable")
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-1",
	})
	assertCode(t, err, codes.Internal)
	if got := len(mock.InstallCalls()); got != 0 {
		t.Errorf("Install called %d times on network discovery failure, want 0", got)
	}
}

func TestHandler_FirewallEnable_HostProxyResolveError_PropagatesInternal(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("nxdomain")
	}
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-1",
	})
	assertCode(t, err, codes.Internal)
	if got := len(mock.InstallCalls()); got != 0 {
		t.Errorf("Install called %d times on resolve failure, want 0", got)
	}
}

func TestHandler_FirewallEnable_DriftDetected_UsesFreshID(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil) // fresh cgroup ID from the fake is testCgroupID

	// Pre-populate stored cgroup ID with a stale value.
	h.cgroupIDMu.Lock()
	h.storedCgroupID["ctr-drift"] = testCgroupID + 1
	h.cgroupIDMu.Unlock()

	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-drift",
	})
	if err != nil {
		t.Fatalf("FirewallEnable returned error: %v", err)
	}

	calls := mock.InstallCalls()
	if len(calls) != 1 {
		t.Fatalf("Install called %d times, want 1", len(calls))
	}
	if calls[0].CgroupID != testCgroupID {
		t.Errorf("Install called with cgroupID=%d, want fresh %d (drift correction failed)", calls[0].CgroupID, testCgroupID)
	}

	h.cgroupIDMu.Lock()
	stored := h.storedCgroupID["ctr-drift"]
	h.cgroupIDMu.Unlock()
	if stored != testCgroupID {
		t.Errorf("storedCgroupID after Enable = %d, want fresh %d", stored, testCgroupID)
	}
}

func TestHandler_FirewallEnable_EBPFError_PropagatesInternal(t *testing.T) {
	mock := noopMock()
	mock.InstallFunc = func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error {
		return errors.New("install failed")
	}
	h := newTestHandler(mock, nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-err",
	})
	assertCode(t, err, codes.Internal)
}

func TestHandler_FirewallEnable_CancelsBypassTimer(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)

	// Plant a bypass entry whose timer would fire if not cancelled.
	const cid = "ctr-bypassed"
	cancelled := make(chan struct{})
	entry := &bypassEntry{
		containerID: cid,
		cgroupID:    99,
		timer: time.AfterFunc(time.Hour, func() {
			close(cancelled)
		}),
	}
	h.bypassTimersMu.Lock()
	h.bypassTimers[cid] = entry
	h.bypassTimersMu.Unlock()

	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: cid,
	})
	if err != nil {
		t.Fatalf("FirewallEnable returned error: %v", err)
	}

	h.bypassTimersMu.Lock()
	_, stillThere := h.bypassTimers[cid]
	h.bypassTimersMu.Unlock()
	if stillThere {
		t.Error("Enable mid-bypass should remove the timer entry")
	}

	select {
	case <-cancelled:
		t.Error("bypass timer fired despite Enable cancellation")
	case <-time.After(50 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------
// FirewallDisable
// ---------------------------------------------------------------------------

func TestHandler_FirewallDisable_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)
	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{ContainerId: "ctr-1"})
	if err != nil {
		t.Fatalf("FirewallDisable returned error: %v", err)
	}
	if got := len(mock.DisableCalls()); got != 1 {
		t.Fatalf("Disable called %d times, want 1", got)
	}
}

func TestHandler_FirewallDisable_EmptyContainerID(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallDisable_ContainerGone_KnownStored_UsesFallback(t *testing.T) {
	mock := noopMock()
	resolver := func(_ context.Context, ref string) (string, string, bool, error) {
		return ref, "", false, nil
	}
	h := newTestHandler(mock, resolver)
	const cid = "ctr-gone"
	h.cgroupIDMu.Lock()
	h.storedCgroupID[cid] = 12345
	h.cgroupIDMu.Unlock()

	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{ContainerId: cid})
	if err != nil {
		t.Fatalf("FirewallDisable returned error: %v", err)
	}
	calls := mock.DisableCalls()
	if len(calls) != 1 || calls[0].CgroupID != 12345 {
		t.Errorf("Disable should use stored cgroup ID 12345, got calls=%v", calls)
	}
}

func TestHandler_FirewallDisable_ContainerUnknown_NoOp(t *testing.T) {
	mock := noopMock()
	resolver := func(_ context.Context, ref string) (string, string, bool, error) {
		return ref, "", false, nil
	}
	h := newTestHandler(mock, resolver)
	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{ContainerId: "never-seen"})
	if err != nil {
		t.Fatalf("FirewallDisable returned error: %v", err)
	}
	if got := len(mock.DisableCalls()); got != 0 {
		t.Errorf("Disable called %d times for unknown gone container, want 0 (no-op)", got)
	}
}

// ---------------------------------------------------------------------------
// FirewallBypass
// ---------------------------------------------------------------------------

func TestHandler_FirewallBypass_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{
		ContainerId: "ctr-1", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatalf("FirewallBypass returned error: %v", err)
	}
	if got := len(mock.DisableCalls()); got != 1 {
		t.Fatalf("Disable called %d times, want 1", got)
	}
	h.bypassTimersMu.Lock()
	_, ok := h.bypassTimers["ctr-1"]
	h.bypassTimersMu.Unlock()
	if !ok {
		t.Error("bypass timer should be tracked under container_id")
	}
	// Stop the timer so it doesn't fire after the test exits.
	h.CancelAllBypassTimers()
}

func TestHandler_FirewallBypass_EmptyContainerID(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{TimeoutSeconds: 30})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallBypass_TimeoutExceedsMax(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{
		ContainerId: "ctr-1", TimeoutSeconds: uint32((maxBypassTimeout + time.Second).Seconds()),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallBypass_ContainerGone_FailedPrecondition(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	h := newTestHandler(noopMock(), resolver)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{
		ContainerId: "ctr-gone", TimeoutSeconds: 30,
	})
	assertCode(t, err, codes.FailedPrecondition)
}

func TestHandler_FirewallBypass_TimerAutoEnables(t *testing.T) {
	enableCalled := make(chan uint64, 1)
	mock := noopMock()
	mock.EnableFunc = func(cgroupID uint64) error {
		enableCalled <- cgroupID
		return nil
	}
	h := newTestHandler(mock, nil)

	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{
		ContainerId: "ctr-auto", TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("FirewallBypass returned error: %v", err)
	}

	select {
	case <-enableCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bypass timer to auto-enable")
	}
}

// FirewallSyncRoutes happy + error paths are covered by the
// authz_test.go gRPC pipeline tests (`TestAdminHandler_SyncRoutes` /
// `TestAdminHandler_SyncRoutes_EBPFError`). Duplicating them here would
// only add a thin direct-call layer over the same ebpf mock — no new
// surface.

// ---------------------------------------------------------------------------
// FirewallResolveHostname
// ---------------------------------------------------------------------------

func TestHandler_FirewallResolveHostname_Success(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	h.resolveHostFn = func(_ context.Context, host string) ([]string, error) {
		if host != "host.docker.internal" {
			t.Fatalf("unexpected host %q", host)
		}
		return []string{"192.168.65.254"}, nil
	}
	resp, err := h.FirewallResolveHostname(context.Background(), &adminv1.FirewallResolveHostnameRequest{
		Hostname: "host.docker.internal",
	})
	if err != nil {
		t.Fatalf("FirewallResolveHostname returned error: %v", err)
	}
	if got := resp.GetAddresses(); len(got) != 1 || got[0] != "192.168.65.254" {
		t.Errorf("addresses = %v, want [192.168.65.254]", got)
	}
}

func TestHandler_FirewallResolveHostname_Empty(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallResolveHostname(context.Background(), &adminv1.FirewallResolveHostnameRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallResolveHostname_DNSError(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("nxdomain")
	}
	_, err := h.FirewallResolveHostname(context.Background(), &adminv1.FirewallResolveHostnameRequest{
		Hostname: "missing.example.com",
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// FirewallInit / FirewallReload / FirewallStatus — drive the fake stack.
// ---------------------------------------------------------------------------

// TestHandler_StackRPCs_DelegateToStack covers the three stack-facing RPCs
// that just forward to a Stack method: FirewallInit → EnsureRunning,
// FirewallReload → Reload, FirewallStatus → Status. One table, one
// per-RPC assertion that the stack was called exactly once — response
// shapes are built from the fake's statusResult, so field round-trips
// would be tautological.
func TestHandler_StackRPCs_DelegateToStack(t *testing.T) {
	tests := []struct {
		name     string
		call     func(h *Handler) error
		getCalls func(s *fakeStack) int
	}{
		{
			name: "FirewallInit",
			call: func(h *Handler) error {
				_, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
				return err
			},
			getCalls: func(s *fakeStack) int { return s.ensureRunningCalls },
		},
		{
			name: "FirewallReload",
			call: func(h *Handler) error {
				_, err := h.FirewallReload(context.Background(), &adminv1.FirewallReloadRequest{})
				return err
			},
			getCalls: func(s *fakeStack) int { return s.reloadCalls },
		},
		{
			name: "FirewallStatus",
			call: func(h *Handler) error {
				_, err := h.FirewallStatus(context.Background(), &adminv1.FirewallStatusRequest{})
				return err
			},
			getCalls: func(s *fakeStack) int { return s.statusCalls },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stack := &fakeStack{}
			h := NewHandler(HandlerDeps{
				EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(),
			})
			require.NoError(t, tc.call(h))
			assert.Equal(t, 1, tc.getCalls(stack), "stack method called exactly once")
		})
	}
}

func TestHandler_FirewallInit_StackFailure_PropagatesInternal(t *testing.T) {
	stack := &fakeStack{ensureErr: errors.New("docker down")}
	h := NewHandler(HandlerDeps{
		EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(),
	})
	_, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// resolveBypassCgroupID branches (drift helper)
// ---------------------------------------------------------------------------

// fakeCgroupIDFn builds a cgroupIDFn that returns id for any path.
func fakeCgroupIDFn(id uint64) func(string) (uint64, error) {
	return func(string) (uint64, error) { return id, nil }
}

// errCgroupIDFn builds a cgroupIDFn that always fails — drives the
// stat-failure fallback branch.
func errCgroupIDFn(err error) func(string) (uint64, error) {
	return func(string) (uint64, error) { return 0, err }
}

func TestResolveBypassCgroupID_EmptyContainerID_FallsBack(t *testing.T) {
	entry := &bypassEntry{cgroupID: 99999}
	got := resolveBypassCgroupID(entry, nopResolver, fakeCgroupIDFn(testCgroupID), logger.Nop())
	if got != 99999 {
		t.Errorf("expected fallback to stored cgroup ID 99999, got %d", got)
	}
}

func TestResolveBypassCgroupID_DockerAPIError_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, errors.New("docker unavailable")
	}
	entry := &bypassEntry{containerID: "ctr-docker-err", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, fakeCgroupIDFn(testCgroupID), logger.Nop())
	if got != 12345 {
		t.Errorf("expected fallback to stored cgroup ID 12345, got %d", got)
	}
}

func TestResolveBypassCgroupID_ContainerGone_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	entry := &bypassEntry{containerID: "ctr-gone", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, fakeCgroupIDFn(testCgroupID), logger.Nop())
	if got != 12345 {
		t.Errorf("expected fallback to stored cgroup ID 12345, got %d", got)
	}
}

func TestResolveBypassCgroupID_ContainerAlive_ReturnsFreshID(t *testing.T) {
	const freshID = uint64(7777)
	entry := &bypassEntry{containerID: "ctr-alive", cgroupID: freshID}
	got := resolveBypassCgroupID(entry, nopResolver, fakeCgroupIDFn(freshID), logger.Nop())
	if got != freshID {
		t.Errorf("expected fresh cgroup ID %d, got %d", freshID, got)
	}
}

func TestResolveBypassCgroupID_DriftDetected_UsesFresh(t *testing.T) {
	const freshID = uint64(7777)
	entry := &bypassEntry{containerID: "ctr-drift", cgroupID: freshID + 1}
	got := resolveBypassCgroupID(entry, nopResolver, fakeCgroupIDFn(freshID), logger.Nop())
	if got != freshID {
		t.Errorf("expected fresh cgroup ID %d (drift from stored %d), got %d", freshID, entry.cgroupID, got)
	}
}

func TestResolveBypassCgroupID_CgroupStatFails_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "ref", testCgroupPath, true, nil
	}
	entry := &bypassEntry{containerID: "ctr-stat-fail", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, errCgroupIDFn(errors.New("stat failed")), logger.Nop())
	if got != 12345 {
		t.Errorf("expected fallback to stored cgroup ID 12345, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// bypassTimerFired retry behaviour
// ---------------------------------------------------------------------------

func TestBypassTimerFired_AllRetriesExhausted_CleansUpEntry(t *testing.T) {
	mock := noopMock()
	mock.EnableFunc = func(_ uint64) error {
		return errors.New("enable always fails")
	}
	h := newTestHandler(mock, nil)

	const cid = "ctr-retry-exhaust"
	entry := &bypassEntry{
		containerID: cid,
		cgroupID:    12345,
		timer:       time.NewTimer(time.Hour),
	}
	h.bypassTimersMu.Lock()
	h.bypassTimers[cid] = entry
	h.bypassTimersMu.Unlock()

	h.bypassTimerFired(cid, entry)

	h.bypassTimersMu.Lock()
	_, exists := h.bypassTimers[cid]
	h.bypassTimersMu.Unlock()
	if exists {
		t.Error("bypass timer entry should be cleaned up after all retries exhausted")
	}
	if got := len(mock.EnableCalls()); got != 3 {
		t.Errorf("Enable called %d times, want 3", got)
	}
}

func TestBypassTimerFired_SucceedsOnRetry_CleansUpEntry(t *testing.T) {
	var calls int
	mock := noopMock()
	mock.EnableFunc = func(_ uint64) error {
		calls++
		if calls == 1 {
			return errors.New("transient failure")
		}
		return nil
	}
	h := newTestHandler(mock, nil)

	const cid = "ctr-retry-succeed"
	entry := &bypassEntry{
		containerID: cid,
		cgroupID:    12345,
		timer:       time.NewTimer(time.Hour),
	}
	h.bypassTimersMu.Lock()
	h.bypassTimers[cid] = entry
	h.bypassTimersMu.Unlock()

	h.bypassTimerFired(cid, entry)

	h.bypassTimersMu.Lock()
	_, exists := h.bypassTimers[cid]
	h.bypassTimersMu.Unlock()
	if exists {
		t.Error("bypass timer entry should be cleaned up after successful retry")
	}
	if calls != 2 {
		t.Errorf("Enable called %d times, want 2", calls)
	}
}

// ---------------------------------------------------------------------------
// CancelAllBypassTimers
// ---------------------------------------------------------------------------

func TestHandler_CancelAllBypassTimers_StopsAndClears(t *testing.T) {
	h := newTestHandler(noopMock(), nil)

	for _, cid := range []string{"ctr-a", "ctr-b", "ctr-c"} {
		h.bypassTimers[cid] = &bypassEntry{
			containerID: cid, cgroupID: 1,
			timer: time.AfterFunc(time.Hour, func() { t.Errorf("timer for %s fired despite cancel", cid) }),
		}
	}
	cancelled := h.CancelAllBypassTimers()
	if cancelled != 3 {
		t.Errorf("cancelled = %d, want 3", cancelled)
	}
	if len(h.bypassTimers) != 0 {
		t.Errorf("bypassTimers should be empty after cancel, got %d", len(h.bypassTimers))
	}
}

// ---------------------------------------------------------------------------
// proto ↔ config rule round-trip
// ---------------------------------------------------------------------------

// TestProtoRulesRoundTrip pins the field map between adminv1.EgressRule/
// PathRule and config.EgressRule/PathRule via full round-trip equality.
// A new field on either side without a matching translator update loses
// data here and fails the test — not just the subset the test happens to
// sample.
func TestProtoRulesRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   []*adminv1.EgressRule
	}{
		{
			name: "tls with path rules",
			in: []*adminv1.EgressRule{{
				Dst: "api.example.com", Proto: "tls", Port: 443, Action: "allow",
				PathRules: []*adminv1.PathRule{
					{Path: "/v1", Action: "allow"},
					{Path: "/admin", Action: "deny"},
				},
				PathDefault: "deny",
			}},
		},
		{
			name: "wildcard dst, no path rules",
			in: []*adminv1.EgressRule{{
				Dst: "*.github.com", Proto: "tls", Port: 443, Action: "allow",
			}},
		},
		{
			name: "http proto, path default only",
			in: []*adminv1.EgressRule{{
				Dst: "plain.example.com", Proto: "http", Port: 80, Action: "allow",
				PathDefault: "deny",
			}},
		},
		{
			name: "multiple rules, mixed protos",
			in: []*adminv1.EgressRule{
				{Dst: "a.example.com", Proto: "tls", Port: 443, Action: "allow"},
				{Dst: "b.example.com", Proto: "tcp", Port: 22, Action: "allow"},
				{Dst: "c.example.com", Proto: "http", Port: 80, Action: "deny"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ConfigRulesToProto(ProtoRulesToConfig(tc.in))
			require.Equal(t, len(tc.in), len(out), "rule count preserved")
			for i, want := range tc.in {
				got := out[i]
				assert.Equal(t, want.GetDst(), got.GetDst(), "Dst")
				assert.Equal(t, want.GetProto(), got.GetProto(), "Proto")
				assert.Equal(t, want.GetPort(), got.GetPort(), "Port")
				assert.Equal(t, want.GetAction(), got.GetAction(), "Action")
				assert.Equal(t, want.GetPathDefault(), got.GetPathDefault(), "PathDefault")
				require.Equal(t, len(want.GetPathRules()), len(got.GetPathRules()), "PathRules len")
				for j, wp := range want.GetPathRules() {
					gp := got.GetPathRules()[j]
					assert.Equal(t, wp.GetPath(), gp.GetPath(), "PathRules[%d].Path", j)
					assert.Equal(t, wp.GetAction(), gp.GetAction(), "PathRules[%d].Action", j)
				}
			}
		})
	}
}
