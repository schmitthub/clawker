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
	"github.com/schmitthub/clawker/internal/consts"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf/mocks"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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
// a fake stack, no rule store, and a live ActionQueue. Tests register
// the queue's Close with t.Cleanup so the worker goroutine exits with
// the test. Tests that exercise rule-store paths build their own
// Handler with a real store via testenv. resolveHostFn is wired to a
// fixed answer so FirewallEnable's host-proxy bypass path (default-
// enabled in the mock config) doesn't hit the real resolver.
func newTestHandler(t *testing.T, mock *ebpfmocks.EBPFManagerMock, resolver ContainerResolver) *Handler {
	t.Helper()
	if resolver == nil {
		resolver = nopResolver
	}
	q := NewActionQueue(nil)
	t.Cleanup(func() { _ = q.Close() })
	h := NewHandler(HandlerDeps{
		EBPF:     mock,
		Stack:    &fakeStack{},
		Cfg:      testConfig(),
		Resolver: resolver,
		Log:      logger.Nop(),
		Queue:    q,
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
	h := newTestHandler(t, mock, nil)

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
	h := newTestHandler(t, noopMock(), nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallEnable_ContainerGone_FailedPrecondition(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	h := newTestHandler(t, noopMock(), resolver)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-gone",
	})
	assertCode(t, err, codes.FailedPrecondition)
}

func TestHandler_FirewallEnable_NetworkInfoError_PropagatesInternal(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(t, mock, nil)
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
	h := newTestHandler(t, mock, nil)
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
	h := newTestHandler(t, mock, nil) // fresh cgroup ID from the fake is testCgroupID

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
	h := newTestHandler(t, mock, nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-err",
	})
	assertCode(t, err, codes.Internal)
}

func TestHandler_FirewallEnable_CancelsBypassTimer(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(t, mock, nil)

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
	h := newTestHandler(t, mock, nil)
	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{ContainerId: "ctr-1"})
	if err != nil {
		t.Fatalf("FirewallDisable returned error: %v", err)
	}
	if got := len(mock.DisableCalls()); got != 1 {
		t.Fatalf("Disable called %d times, want 1", got)
	}
}

func TestHandler_FirewallDisable_EmptyContainerID(t *testing.T) {
	h := newTestHandler(t, noopMock(), nil)
	_, err := h.FirewallDisable(context.Background(), &adminv1.FirewallDisableRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallDisable_ContainerGone_KnownStored_UsesFallback(t *testing.T) {
	mock := noopMock()
	resolver := func(_ context.Context, ref string) (string, string, bool, error) {
		return ref, "", false, nil
	}
	h := newTestHandler(t, mock, resolver)
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
	h := newTestHandler(t, mock, resolver)
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
	h := newTestHandler(t, mock, nil)
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
	h := newTestHandler(t, noopMock(), nil)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{TimeoutSeconds: 30})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallBypass_TimeoutExceedsMax(t *testing.T) {
	h := newTestHandler(t, noopMock(), nil)
	_, err := h.FirewallBypass(context.Background(), &adminv1.FirewallBypassRequest{
		ContainerId: "ctr-1", TimeoutSeconds: uint32((maxBypassTimeout + time.Second).Seconds()),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallBypass_ContainerGone_FailedPrecondition(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	h := newTestHandler(t, noopMock(), resolver)
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
	h := newTestHandler(t, mock, nil)

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
	h := newTestHandler(t, noopMock(), nil)
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
	h := newTestHandler(t, noopMock(), nil)
	_, err := h.FirewallResolveHostname(context.Background(), &adminv1.FirewallResolveHostnameRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallResolveHostname_DNSError(t *testing.T) {
	h := newTestHandler(t, noopMock(), nil)
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
			stack := &fakeStack{statusResult: Status{Running: true}}
			q := NewActionQueue(nil)
			t.Cleanup(func() { _ = q.Close() })
			h := NewHandler(HandlerDeps{
				EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(), Queue: q,
			})
			require.NoError(t, tc.call(h))
			assert.Equal(t, 1, tc.getCalls(stack), "stack method called exactly once")
		})
	}
}

func TestHandler_FirewallInit_StackFailure_PropagatesInternal(t *testing.T) {
	stack := &fakeStack{ensureErr: errors.New("docker down")}
	q := NewActionQueue(nil)
	t.Cleanup(func() { _ = q.Close() })
	h := NewHandler(HandlerDeps{
		EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(), Queue: q,
	})
	_, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	assertCode(t, err, codes.Internal)
}

// TestHandler_FirewallInit_ReenrollsRunningAgents locks in the
// regression fix for the "CP restart leaves running agents unenforced"
// bug: after the firewall stack is healthy, Init must re-Install every
// running managed agent. Without this, the previous CP's FlushAll
// wipes container_map and long-lived agents egress unenforced
// (fail-open by BPF design) until they are restarted or explicitly
// re-enabled.
func TestHandler_FirewallInit_ReenrollsRunningAgents(t *testing.T) {
	stack := &fakeStack{statusResult: Status{Running: true}}
	mock := noopMock()

	// Two agent containers — both resolve to distinct cgroup paths/ids
	// so we can assert Install was called with the right arguments.
	agents := []string{"agent-A", "agent-B"}
	cgroupPaths := map[string]string{
		"agent-A": "/test/cgroup/agent-A",
		"agent-B": "/test/cgroup/agent-B",
	}
	cgroupIDs := map[string]uint64{
		"agent-A": 111,
		"agent-B": 222,
	}
	resolver := func(_ context.Context, ref string) (string, string, bool, error) {
		p, ok := cgroupPaths[ref]
		if !ok {
			return ref, testCgroupPath, true, nil
		}
		return ref, p, true, nil
	}

	q := NewActionQueue(nil)
	t.Cleanup(func() { _ = q.Close() })
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	require.NoError(t, bus.Start(context.Background()))
	t.Cleanup(func() { _ = bus.Close() })
	// Subscribe BEFORE FirewallInit so the publish path is observable.
	// netlogger's LabelCache hydration depends on this — without it
	// every record from a re-enrolled agent carries empty attribution
	// (the bug this regression locks in).
	sub, ok := overseer.Subscribe[ebpf.EBPFContainerEnrolled](bus, "test.reenroll")
	require.True(t, ok, "Subscribe must succeed on a live bus")
	t.Cleanup(sub.Unsubscribe)

	h := NewHandler(HandlerDeps{
		EBPF:       mock,
		Stack:      stack,
		Cfg:        testConfig(),
		Resolver:   resolver,
		Log:        logger.Nop(),
		Queue:      q,
		Bus:        bus,
		ListAgents: func(_ context.Context) ([]string, error) { return agents, nil },
	})
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return []string{"192.168.65.254"}, nil
	}
	h.cgroupIDFn = func(path string) (uint64, error) {
		for cid, p := range cgroupPaths {
			if p == path {
				return cgroupIDs[cid], nil
			}
		}
		return testCgroupID, nil
	}

	_, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	require.NoError(t, err)
	require.Len(t, mock.InstallCalls(), 2, "both agents must be enrolled during FirewallInit")

	// InstallCalls order mirrors the agent list order. Assert per-call
	// cgroup identity so a regression that accidentally installed the
	// same config twice (or swapped indices) shows up clearly.
	gotIDs := []uint64{mock.InstallCalls()[0].CgroupID, mock.InstallCalls()[1].CgroupID}
	assert.ElementsMatch(t, []uint64{111, 222}, gotIDs)

	// Pin the publish: every re-enrolled agent MUST emit an
	// EBPFContainerEnrolled event so netlogger's LabelCache hydrates
	// for cgroups that outlived the previous CP. Drain with a small
	// deadline so a regression that drops the publish fails fast
	// instead of hanging the suite.
	gotEnrolls := map[uint64]string{}
	deadline := time.After(2 * time.Second)
	for len(gotEnrolls) < 2 {
		select {
		case ev := <-sub.C:
			gotEnrolls[ev.CgroupID] = ev.ContainerID
		case <-deadline:
			t.Fatalf("timed out waiting for re-enrollment publish; got %d/%d events: %+v",
				len(gotEnrolls), 2, gotEnrolls)
		}
	}
	assert.Equal(t, "agent-A", gotEnrolls[111])
	assert.Equal(t, "agent-B", gotEnrolls[222])
}

// TestHandler_FirewallInit_ReenrollContinuesOnPerAgentFailure verifies
// the best-effort loop: one broken agent must not block enrollment for
// the rest, and FirewallInit itself must succeed. Stack-wide init is
// strictly more valuable than perfect per-container coverage — any
// agent we failed to enroll stays fail-open exactly as it was before
// Init ran.
func TestHandler_FirewallInit_ReenrollContinuesOnPerAgentFailure(t *testing.T) {
	stack := &fakeStack{statusResult: Status{Running: true}}
	mock := noopMock()
	installCalls := 0
	mock.InstallFunc = func(cgroupID uint64, _ string, _ ebpf.BPFContainerConfig) error {
		installCalls++
		if cgroupID == 111 {
			return errors.New("cgroup 111 broken")
		}
		return nil
	}
	resolver := func(_ context.Context, ref string) (string, string, bool, error) {
		return ref, "/test/cgroup/" + ref, true, nil
	}
	q := NewActionQueue(nil)
	t.Cleanup(func() { _ = q.Close() })
	h := NewHandler(HandlerDeps{
		EBPF:       mock,
		Stack:      stack,
		Cfg:        testConfig(),
		Resolver:   resolver,
		Log:        logger.Nop(),
		Queue:      q,
		ListAgents: func(_ context.Context) ([]string, error) { return []string{"broken", "healthy"}, nil },
	})
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return []string{"192.168.65.254"}, nil
	}
	h.cgroupIDFn = func(path string) (uint64, error) {
		if path == "/test/cgroup/broken" {
			return 111, nil
		}
		return 222, nil
	}

	_, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	require.NoError(t, err, "per-agent failure must not fail Init")
	assert.Equal(t, 2, installCalls, "both agents attempted; failure on one does not short-circuit")
}

// TestHandler_FirewallInit_SyncsRoutesFromStore is the regression
// test for the "route_map empty after CP restart" bug. Sequence:
//
//  1. CP container restarts; egress-rules.yaml on disk persists every
//     prior rule.
//  2. CLI calls FirewallInit during container start.
//  3. FirewallAddRules with the same project rules dedups everything
//     against the persisted store and short-circuits with added=0
//     before reconcileStackClosure runs — so SyncRoutes is skipped on
//     that path.
//  4. Without this fix, route_map stays empty: BPF connect4 lookups
//     miss, traffic falls through to the default Envoy redirect,
//     non-TLS egress (e.g. SSH:22) hits the TLS listener and resets.
//
// FirewallInit owns the post-bringup route sync because it is the
// only RPC that brings up a fresh stack against an already-persisted
// rules store.
func TestHandler_FirewallInit_SyncsRoutesFromStore(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	// Pre-seed the store via the handler's own helper so the rules land
	// on disk exactly as a prior CP run would have left them.
	statuses, err := h.addRulesToStore([]config.EgressRule{
		{Dst: "github.com", Proto: "ssh", Port: "22", Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"},
	})
	require.NoError(t, err)
	require.Len(t, statuses, 2, "one status per input rule")
	for _, s := range statuses {
		require.Equal(t, addStatusAdded, s, "both rules are brand-new")
	}
	// addRulesToStore writes to disk only; no Submit, so no SyncRoutes
	// calls have been recorded yet.
	require.Empty(t, mock.SyncRoutesCalls(), "store seed must not invoke SyncRoutes")

	_, err = h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	require.NoError(t, err)

	calls := mock.SyncRoutesCalls()
	require.Len(t, calls, 1, "FirewallInit must SyncRoutes exactly once during bringup")

	routes := calls[0].Routes
	require.Len(t, routes, 3, "ssh route + https TCP egress route + https QUIC/h3 UDP route")

	// Locate each route by (DstPort, l4_proto). The https rule projects two:
	// the TCP egress route and the QUIC/h3 (UDP) sibling on the same port.
	var sshRoute, tlsRoute, quicRoute *ebpf.Route
	for i := range routes {
		switch {
		case routes[i].DstPort == 22:
			sshRoute = &routes[i]
		case routes[i].DstPort == 443 && routes[i].L4Proto == ebpf.L4ProtoTCP:
			tlsRoute = &routes[i]
		case routes[i].DstPort == 443 && routes[i].L4Proto == ebpf.L4ProtoUDP:
			quicRoute = &routes[i]
		}
	}
	require.NotNil(t, sshRoute, "SSH rule missing from route_map seed")
	require.NotNil(t, tlsRoute, "TLS rule missing from route_map seed")
	require.NotNil(t, quicRoute, "QUIC/h3 sibling missing from route_map seed")
	assert.NotZero(t, sshRoute.DomainHash)
	assert.NotZero(t, tlsRoute.DomainHash)
	assert.Equal(t, tlsRoute.DomainHash, quicRoute.DomainHash, "QUIC sibling shares the https domain hash")

	// TLS + QUIC/h3 both target the main Envoy egress listener (TCP and UDP on
	// the same port). SSH routes to a dedicated per-rule TCP listener at
	// TCPPortBase + idx. A refactor that flipped these port assignments would
	// silently misroute traffic through the wrong listener type (e.g. SSH
	// reaching the TLS listener, which tls_inspector would reject).
	assert.Equal(t, uint16(consts.EnvoyEgressPort), tlsRoute.EnvoyPort,
		"TLS rule must target the main egress listener port")
	assert.Equal(t, uint16(consts.EnvoyEgressPort), quicRoute.EnvoyPort,
		"QUIC/h3 sibling must target the egress (QUIC) listener port")
	assert.GreaterOrEqual(t, sshRoute.EnvoyPort, uint16(consts.EnvoyTCPPortBase),
		"SSH rule must target a dedicated TCP listener port")
}

// TestHandler_FirewallInit_EmitsNormalizeWarnings covers the warning
// path introduced by routesFromStore: rules that normalize away (or
// dedup against another) produce a normalize_warning log but do NOT
// block the route_map seed. Pre-fix, these warnings were a bare
// Msg(w) with no structured field — making them unsearchable in
// production logs. The new contract is a structured log per warning
// + a still-valid SyncRoutes call for the survivors.
func TestHandler_FirewallInit_EmitsNormalizeWarningsButSyncsSurvivors(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	// Two rules that normalize to the same key → first lands as ADDED,
	// the second collides on the same RuleKey and reports UNCHANGED
	// (identical re-apply after the first insert).
	statuses, err := h.addRulesToStore([]config.EgressRule{
		{Dst: "Example.Com", Proto: "https", Port: "443", Action: "allow"},
		{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"},
	})
	require.NoError(t, err)
	require.Len(t, statuses, 2, "one status per input rule even when keys collide")

	_, err = h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	require.NoError(t, err)

	calls := mock.SyncRoutesCalls()
	require.Len(t, calls, 1)
	assert.NotEmpty(t, calls[0].Routes, "surviving rule must still produce a route")
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

// bypassTimerFired makes a single enqueue attempt; retrying from a
// timer goroutine would block shutdown and add little value — the
// operator can reissue FirewallEnable. A permanent enable failure
// logs + cleans up the entry.
func TestBypassTimerFired_EnableFails_CleansUpEntry(t *testing.T) {
	mock := noopMock()
	mock.EnableFunc = func(_ uint64) error {
		return errors.New("enable always fails")
	}
	h := newTestHandler(t, mock, nil)

	const cid = "ctr-enable-fail"
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
		t.Error("bypass timer entry should be cleaned up after enable failure")
	}
	if got := len(mock.EnableCalls()); got != 1 {
		t.Errorf("Enable called %d times, want 1 (single-attempt failsafe)", got)
	}
}

// Successful single attempt cleans up the entry and leaves no orphan
// state.
func TestBypassTimerFired_EnableSucceeds_CleansUpEntry(t *testing.T) {
	var calls int
	mock := noopMock()
	mock.EnableFunc = func(_ uint64) error {
		calls++
		return nil
	}
	h := newTestHandler(t, mock, nil)

	const cid = "ctr-enable-ok"
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
		t.Error("bypass timer entry should be cleaned up after successful enable")
	}
	if calls != 1 {
		t.Errorf("Enable called %d times, want 1", calls)
	}
}

// ---------------------------------------------------------------------------
// CancelAllBypassTimers
// ---------------------------------------------------------------------------

func TestHandler_CancelAllBypassTimers_StopsAndClears(t *testing.T) {
	h := newTestHandler(t, noopMock(), nil)

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
// Queue behavior: coalescing, FIFO, sentinels
// ---------------------------------------------------------------------------

// ruleStoreHandler builds a Handler wired with a real rules store on an
// isolated filesystem so tests can exercise the full add/remove/reload
// paths end-to-end. The returned fakeStack reports Running=true by
// default so reconcileStackClosure takes the reload branch; tests flip
// statusResult.Running = false to exercise the down-stack path.
func ruleStoreHandler(t *testing.T, mock *ebpfmocks.EBPFManagerMock) (*Handler, *fakeStack) {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := NewRulesStore(cfg)
	require.NoError(t, err)

	stack := &fakeStack{statusResult: Status{Running: true}}
	q := NewActionQueue(nil)
	t.Cleanup(func() { _ = q.Close() })
	h := NewHandler(HandlerDeps{
		EBPF:     mock,
		Stack:    stack,
		Store:    store,
		Cfg:      cfg,
		Resolver: nopResolver,
		Log:      logger.Nop(),
		Queue:    q,
	})
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return []string{"192.168.65.254"}, nil
	}
	h.cgroupIDFn = func(string) (uint64, error) { return testCgroupID, nil }
	return h, stack
}

// TestHandler_Reload_CallsSyncRoutesAfterReload verifies the
// reconcileStackClosure runs Stack.Reload AND ebpf.SyncRoutes in order.
// This is the "SyncRoutes bug" fix from the initiative: the old
// FirewallAddRules/RemoveRules called Stack.Reload but NOT
// ebpf.SyncRoutes, leaving route_map stale after rule mutations.
func TestHandler_Reload_CallsSyncRoutesAfterReload(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, stack.reloadCalls, "Stack.Reload called once per reconcile")
	require.Len(t, mock.SyncRoutesCalls(), 1, "ebpf.SyncRoutes called exactly once per reconcile")

	// Routes derived from the store (not from req) and carry EnvoyPort from
	// cfg. An https rule projects BOTH the TCP egress route and the QUIC/h3
	// (UDP) sibling — same domain+port+EnvoyPort, distinct l4_proto.
	routes := mock.SyncRoutesCalls()[0].Routes
	require.Len(t, routes, 2)
	for _, rt := range routes {
		assert.NotZero(t, rt.DomainHash)
		assert.Equal(t, uint16(443), rt.DstPort)
	}
	assert.ElementsMatch(t,
		[]uint8{ebpf.L4ProtoTCP, ebpf.L4ProtoUDP},
		[]uint8{routes[0].L4Proto, routes[1].L4Proto},
		"https rule must project both the TCP egress route and the QUIC/h3 UDP route")
}

// TestHandler_AddRules_StackDown_PersistsWithoutRestart proves the
// partial-success semantic: when the stack is down, the rule still
// lands in the store and the RPC returns stack_restarted=false rather
// than failing.
func TestHandler_AddRules_StackDown_PersistsWithoutRestart(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)
	stack.statusResult = Status{Running: false}

	resp, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "durable.example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)
	assert.Equal(t,
		[]adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED},
		resp.GetStatuses(),
		"single durable rule reports ADDED")
	assert.False(t, resp.GetStackRestarted(), "stack was down — no restart fired")

	// Rule is still in the store for the next firewall up.
	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetRules(), 1)
	assert.Equal(t, "durable.example.com", listResp.GetRules()[0].GetDst())

	// No Reload should have fired since stack was down.
	assert.Equal(t, 0, stack.reloadCalls)
	assert.Empty(t, mock.SyncRoutesCalls(), "SyncRoutes skipped when stack is down")
}

// TestHandler_AddRules_InvalidDomain_ReturnsRuleInvalid verifies the
// pre-Submit validation path: a bad destination aborts before the store
// write and never queues a reconcile.
func TestHandler_AddRules_InvalidDomain_ReturnsRuleInvalid(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "INVALID UPPERCASE", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.Error(t, err)
	assertCode(t, err, codes.InvalidArgument)
	assertReason(t, err, ReasonRuleInvalid)

	assert.Equal(t, 0, stack.reloadCalls, "invalid rule must not trigger a reconcile")
	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	assert.Empty(t, listResp.GetRules(), "invalid rule must not land in the store")
}

// TestHandler_FirewallRemove_PreservesRulesStore is the teardown-
// semantic fix from the initiative. A user who runs `firewall remove
// evil.com` after `firewall down` must see that removal reflected when
// the firewall comes back up — which requires the store to survive
// teardown.
func TestHandler_FirewallRemove_PreservesRulesStore(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	// Seed a rule while the stack is running.
	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "keep.example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, stack.reloadCalls)

	// Tear down — rules file MUST survive.
	_, err = h.FirewallRemove(context.Background(), &adminv1.FirewallRemoveRequest{})
	require.NoError(t, err)

	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetRules(), 1, "rules store must survive teardown (trailing-mutation invariant)")
	assert.Equal(t, "keep.example.com", listResp.GetRules()[0].GetDst())
}

// TestHandler_AllResolvableDomains_MatchesCorefileZoneSet asserts the
// netlogger-facing reverse-DNS source returns exactly the zone set
// GenerateCorefile would build: internal hosts (docker.internal +
// monitoring service hostnames) union with allow-rule destinations,
// stripping IPs/CIDRs and deny rules, deduped against the reserved
// internal set. The two sources must agree by construction so a
// netlogger record's dst_host lookup matches the dnsbpf hash write.
func TestHandler_AllResolvableDomains_MatchesCorefileZoneSet(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{
			{Dst: "github.com", Proto: "https", Port: "443", Action: "allow"},
			{Dst: ".example.com", Proto: "https", Port: "443", Action: "allow"},    // wildcard, normalized
			{Dst: "203.0.113.5", Proto: "tcp", Port: "22", Action: "allow"},        // IP, skipped
			{Dst: "blocked.test", Proto: "https", Port: "443", Action: "deny"},     // deny, skipped
			{Dst: "docker.internal", Proto: "https", Port: "443", Action: "allow"}, // reserved, dedup
		},
	})
	require.NoError(t, err)

	got := h.AllResolvableDomains()

	// Must include the two real allow-rule domains AND every internal host.
	want := map[string]bool{
		"github.com":      true,
		"example.com":     true, // ".example.com" normalized
		"docker.internal": true,
	}
	for _, h := range consts.MonitoringServiceHostnames {
		want[h] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, d := range got {
		gotSet[d] = true
	}
	for d := range want {
		assert.Truef(t, gotSet[d], "expected %q in AllResolvableDomains; got %v", d, got)
	}
	// Must NOT include IP / CIDR / deny destinations.
	assert.False(t, gotSet["203.0.113.5"], "IP destination must be filtered")
	assert.False(t, gotSet["blocked.test"], "deny rule must be filtered")
}

func TestHandler_AllResolvableDomains_NoStoreReturnsInternalHostsOnly(t *testing.T) {
	// Handler built without a rules store (newTestHandler) returns just
	// the internal hosts — no rules to enumerate, but the netlogger
	// reverse map still needs to attribute internal-zone resolutions.
	mock := noopMock()
	h := newTestHandler(t, mock, nil)

	got := h.AllResolvableDomains()

	gotSet := make(map[string]bool, len(got))
	for _, d := range got {
		gotSet[d] = true
	}
	assert.Truef(t, gotSet["docker.internal"], "internal hosts must be present even without a store; got %v", got)
	for _, host := range consts.MonitoringServiceHostnames {
		assert.Truef(t, gotSet[host], "monitoring hostname %q missing; got %v", host, got)
	}
}

// TestHandler_RemoveRule_Success covers the happy path: rule matches
// exactly, store is mutated, reconcile fires.
func TestHandler_RemoveRule_Success(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)
	reloadsBefore := stack.reloadCalls

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "example.com", Proto: "https", Port: "443",
	})
	require.NoError(t, err)
	assert.True(t, resp.GetStackRestarted())
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, resp.GetStatus(),
		"whole-rule removal reports REMOVED")
	assert.Equal(t, reloadsBefore+1, stack.reloadCalls, "reconcile fires on successful remove")

	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	assert.Empty(t, listResp.GetRules(), "rule removed from store")
}

// TestHandler_RemoveRule_NotFound_Typo is the whole reason this RPC
// shrunk: a typo in the dst MUST surface as Status=NOT_FOUND so the CLI
// can render a failure, never a bogus "Removed rule: exmaple.com". The
// gRPC call succeeds (err == nil) — NOT_FOUND now travels on the
// response, not as codes.NotFound.
func TestHandler_RemoveRule_NotFound_Typo(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)
	reloadsBefore := stack.reloadCalls

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "exmaple.com", Proto: "https", Port: "443",
	})
	require.NoError(t, err)
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, resp.GetStatus())
	assert.False(t, resp.GetStackRestarted(), "no reconcile fires on miss")

	assert.Equal(t, reloadsBefore, stack.reloadCalls, "miss must not trigger reconcile")
	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.Len(t, listResp.GetRules(), 1, "original rule untouched on miss")
	assert.Equal(t, "example.com", listResp.GetRules()[0].GetDst())
}

// TestHandler_RemoveRule_NotFound_WrongProto covers the second failure
// mode the user called out: stored example.com:tcp:80, asked to remove
// example.com:tls:443 — proto+port disagree so the key misses and the
// response carries Status=NOT_FOUND.
func TestHandler_RemoveRule_NotFound_WrongProto(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "example.com", Proto: "tcp", Port: "80", Action: "allow"}},
	})
	require.NoError(t, err)

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "example.com", Proto: "https", Port: "443",
	})
	require.NoError(t, err)
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, resp.GetStatus())

	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.Len(t, listResp.GetRules(), 1, "tcp:80 rule untouched when tls:443 requested")
}

// TestHandler_RemoveRule_StackDown_PersistsRemoval mirrors AddRules's
// stack-down path: removal is durable even when the stack is down, and
// stack_restarted reports false so the CLI can emit the "takes effect
// next firewall up" note.
func TestHandler_RemoveRule_StackDown_PersistsRemoval(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{Dst: "example.com", Proto: "https", Port: "443", Action: "allow"}},
	})
	require.NoError(t, err)

	stack.statusResult = Status{Running: false}
	reloadsBefore := stack.reloadCalls

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "example.com", Proto: "https", Port: "443",
	})
	require.NoError(t, err)
	assert.False(t, resp.GetStackRestarted())
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, resp.GetStatus(),
		"removal is durable even when stack is down")
	assert.Equal(t, reloadsBefore, stack.reloadCalls, "stack down: no reload")

	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	assert.Empty(t, listResp.GetRules(), "removal durable on disk")
}

// assertReason inspects a gRPC status error and asserts that at least
// one errdetails.ErrorInfo carries the expected Reason string. Keeps
// the CLI wire contract verified: CLI dispatches on Reason, not Go-side
// sentinel identity.
func assertReason(t *testing.T, err error, wantReason string) {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %T: %v", err, err)
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetReason() == wantReason {
			return
		}
	}
	t.Fatalf("no errdetails.ErrorInfo with Reason=%q found (got %+v)", wantReason, st.Details())
}

// TestHandler_AddRules_KeyCollision_MergesPathRules is the regression test
// for the path_rules-propagation bug. Pre-seeding the store with a path
// rule, then re-sending the same key with a different path rule, must
// produce a rule whose PathRules list contains BOTH entries — not the
// first-write-wins skip the old addRulesToStore exhibited.
func TestHandler_AddRules_KeyCollision_MergesPathRules(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
		}},
	})
	require.NoError(t, err)
	reloadsBefore := stack.reloadCalls

	resp, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{{Path: "/v2", Action: "deny"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t,
		[]adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED},
		resp.GetStatuses(),
		"merge mutated existing key → MODIFIED")
	assert.Equal(t, reloadsBefore+1, stack.reloadCalls, "merge mutation must trigger one reconcile")

	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetRules(), 1, "still one entry; same RuleKey")
	got := listResp.GetRules()[0]
	require.Len(t, got.GetPathRules(), 2, "both path rules persisted")
	assert.Equal(t, "/v1", got.GetPathRules()[0].GetPath())
	assert.Equal(t, "/v2", got.GetPathRules()[1].GetPath())
	assert.Equal(t, "deny", got.GetPathRules()[1].GetAction())
}

// TestHandler_AddRules_KeyCollision_NoChange_NoReconcile asserts a
// re-seed of identical rules is a true no-op: per-rule status reports
// UNCHANGED, no Stack.Reload, no ebpf.SyncRoutes calls. This guards
// against unnecessary churn on every container start.
func TestHandler_AddRules_KeyCollision_NoChange_NoReconcile(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	rule := &adminv1.EgressRule{
		Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
		PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
	}
	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{rule},
	})
	require.NoError(t, err)
	reloadsBefore := stack.reloadCalls
	syncBefore := len(mock.SyncRoutesCalls())

	resp, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{rule},
	})
	require.NoError(t, err)
	assert.Equal(t,
		[]adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED},
		resp.GetStatuses(),
		"identical re-seed is UNCHANGED")
	assert.Equal(t, reloadsBefore, stack.reloadCalls, "no reconcile on no-op")
	assert.Equal(t, syncBefore, len(mock.SyncRoutesCalls()), "no SyncRoutes on no-op")
}

// TestHandler_AddRules_DenyCarvedOpaqueRange_ReseedUnchanged guards the
// no-op detection for carved opaque rules. An opaque allow range overlapping a
// deny is split by NormalizeAndDedup into per-span rules in the store, so a
// re-add of the ORIGINAL range matches no carved key. Keying the no-op gate off
// per-rule RuleKey reported a spurious ADDED + reconcile on every bootstrap
// re-seed; the gate now compares the canonical before/after, so an identical
// re-seed stays a true no-op.
func TestHandler_AddRules_DenyCarvedOpaqueRange_ReseedUnchanged(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	// Seed an opaque allow range with an overlapping deny — the store carves
	// 45-50 allow into [45-46, 48-50] and keeps 47 deny.
	seed := []*adminv1.EgressRule{
		{Dst: "vpn.example.com", Proto: "tcp", Port: "45-50", Action: "allow"},
		{Dst: "vpn.example.com", Proto: "tcp", Port: "47", Action: "deny"},
	}
	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{Rules: seed})
	require.NoError(t, err)
	reloadsBefore := stack.reloadCalls
	syncBefore := len(mock.SyncRoutesCalls())

	// Re-seed the identical batch (the bootstrap path on every container start).
	resp, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{Rules: seed})
	require.NoError(t, err)
	for i, st := range resp.GetStatuses() {
		assert.Equalf(t, adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED, st,
			"rule %d of a carved re-seed must be UNCHANGED", i)
	}
	assert.Equal(t, reloadsBefore, stack.reloadCalls, "carved re-seed must not reconcile")
	assert.Equal(t, syncBefore, len(mock.SyncRoutesCalls()), "carved re-seed must not SyncRoutes")
}

// TestHandler_AddRules_InvalidRule_FailsLaunch asserts that a malformed port
// or an inverted/typo'd action from clawker.yaml is rejected up front — the
// error rides the RPC back to the CLI and fails the launch — instead of being
// accepted as ADDED then silently dropped at the NormalizeAndDedup reconcile.
func TestHandler_AddRules_InvalidRule_FailsLaunch(t *testing.T) {
	cases := []struct {
		name string
		rule *adminv1.EgressRule
	}{
		{"malformed port range", &adminv1.EgressRule{Dst: "host.example.com", Proto: "tcp", Port: "9100-9000", Action: "allow"}},
		{"non-numeric port", &adminv1.EgressRule{Dst: "host.example.com", Proto: "tcp", Port: "44x", Action: "allow"}},
		{"typo'd action", &adminv1.EgressRule{Dst: "host.example.com", Proto: "https", Port: "443", Action: "dney"}},
		{"typo'd path_default", &adminv1.EgressRule{Dst: "host.example.com", Proto: "https", Port: "443", PathDefault: "denied"}},
		{"empty path rule path", &adminv1.EgressRule{Dst: "host.example.com", Proto: "https", Port: "443", PathRules: []*adminv1.PathRule{{Path: "  ", Action: "allow"}}}},
		{"typo'd path rule action", &adminv1.EgressRule{Dst: "host.example.com", Proto: "https", Port: "443", PathRules: []*adminv1.PathRule{{Path: "/x", Action: "dney"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := noopMock()
			h, stack := ruleStoreHandler(t, mock)

			_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
				Rules: []*adminv1.EgressRule{tc.rule},
			})
			require.Error(t, err)
			assertCode(t, err, codes.InvalidArgument)
			assertReason(t, err, ReasonRuleInvalid)

			assert.Equal(t, 0, stack.reloadCalls, "invalid rule must not reconcile")
			listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
			assert.Empty(t, listResp.GetRules(), "invalid rule must not land in the store")
		})
	}
}

// TestHandler_AddRules_MixedBatch_ReportsPerRuleStatus is the
// bootstrap-shaped case: a batch carrying [new, identical-reseed,
// merge-mutation] must come back with statuses [ADDED, UNCHANGED,
// MODIFIED] in input order. Order preservation is the wire contract
// statuses[i] ↔ req.rules[i] on which the CLI message bank depends.
func TestHandler_AddRules_MixedBatch_ReportsPerRuleStatus(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	// Pre-seed: one rule that will get re-applied identically + one that
	// will be merged with new path rule on next batch.
	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{
			{Dst: "stable.example.com", Proto: "https", Port: "443", Action: "allow"},
			{
				Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
				PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
			},
		},
	})
	require.NoError(t, err)

	// Batch with [new key, identical-reseed of stable, merge add /v2 on
	// api]. Status slice must mirror request order exactly.
	resp, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{
			{Dst: "new.example.com", Proto: "https", Port: "443", Action: "allow"},
			{Dst: "stable.example.com", Proto: "https", Port: "443", Action: "allow"},
			{
				Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
				PathRules: []*adminv1.PathRule{{Path: "/v2", Action: "deny"}},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []adminv1.AddRuleStatus{
		adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED,
		adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED,
		adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED,
	}, resp.GetStatuses(), "statuses[i] mirrors rules[i] in input order")
}

// TestHandler_AddRules_KeyCollision_PathRuleAction_CallerWins asserts the
// same-Path caller-wins semantic at the handler boundary.
func TestHandler_AddRules_KeyCollision_PathRuleAction_CallerWins(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
		}},
	})
	require.NoError(t, err)

	_, err = h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "deny"}},
		}},
	})
	require.NoError(t, err)

	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetRules(), 1)
	got := listResp.GetRules()[0]
	require.Len(t, got.GetPathRules(), 1, "same Path collapses, no duplicate")
	assert.Equal(t, "deny", got.GetPathRules()[0].GetAction(), "caller wins on same-Path collision")
}

// TestHandler_RemoveRule_WithPath_RemovesOnlyPathRule asserts the
// path-scoped removal leaves the rule itself in place and removes only
// the matching PathRule entry.
func TestHandler_RemoveRule_WithPath_RemovesOnlyPathRule(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{
				{Path: "/v1", Action: "allow"},
				{Path: "/v2", Action: "deny"},
			},
		}},
	})
	require.NoError(t, err)

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Path: "/v1",
	})
	require.NoError(t, err)
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED, resp.GetStatus(),
		"path-scoped removal reports PATH_REMOVED")

	listResp, err := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetRules(), 1, "rule itself remains")
	got := listResp.GetRules()[0]
	require.Len(t, got.GetPathRules(), 1, "only /v1 path rule removed")
	assert.Equal(t, "/v2", got.GetPathRules()[0].GetPath())
}

// TestHandler_RemoveRule_WithPath_RuleMissing_NotFound asserts removal
// against a non-existent (dst, proto, port) returns Status=NOT_FOUND on
// the response regardless of the path argument.
func TestHandler_RemoveRule_WithPath_RuleMissing_NotFound(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "missing.example.com", Proto: "https", Port: "443",
		Path: "/v1",
	})
	require.NoError(t, err)
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, resp.GetStatus())
}

// TestHandler_RemoveRule_WithPath_PathMissing_NotFound asserts the
// rule-exists-but-no-such-path case also surfaces Status=NOT_FOUND
// rather than silently succeeding.
func TestHandler_RemoveRule_WithPath_PathMissing_NotFound(t *testing.T) {
	mock := noopMock()
	h, _ := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
		}},
	})
	require.NoError(t, err)

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Path: "/nosuch",
	})
	require.NoError(t, err)
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, resp.GetStatus())

	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.Len(t, listResp.GetRules(), 1, "untouched")
	require.Len(t, listResp.GetRules()[0].GetPathRules(), 1, "untouched")
}

// TestHandler_RemoveRule_WithPath_StackDown_PersistsRemoval mirrors the
// whole-rule stack-down test for the path-scoped branch: the path rule is
// removed durably on disk and stack_restarted reports false rather than
// firing a Reload against a down stack.
func TestHandler_RemoveRule_WithPath_StackDown_PersistsRemoval(t *testing.T) {
	mock := noopMock()
	h, stack := ruleStoreHandler(t, mock)

	_, err := h.FirewallAddRules(context.Background(), &adminv1.FirewallAddRulesRequest{
		Rules: []*adminv1.EgressRule{{
			Dst: "api.example.com", Proto: "https", Port: "443", Action: "allow",
			PathRules: []*adminv1.PathRule{
				{Path: "/v1", Action: "allow"},
				{Path: "/v2", Action: "deny"},
			},
		}},
	})
	require.NoError(t, err)

	stack.statusResult = Status{Running: false}
	reloadsBefore := stack.reloadCalls

	resp, err := h.FirewallRemoveRule(context.Background(), &adminv1.FirewallRemoveRuleRequest{
		Dst: "api.example.com", Proto: "https", Port: "443",
		Path: "/v1",
	})
	require.NoError(t, err)
	assert.False(t, resp.GetStackRestarted())
	assert.Equal(t, adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED, resp.GetStatus(),
		"single path-rule removed → PATH_REMOVED")
	assert.Equal(t, reloadsBefore, stack.reloadCalls, "stack down: no reload")

	listResp, _ := h.FirewallListRules(context.Background(), &adminv1.FirewallListRulesRequest{})
	require.Len(t, listResp.GetRules(), 1, "rule entry persists")
	require.Len(t, listResp.GetRules()[0].GetPathRules(), 1, "removal durable on disk")
	assert.Equal(t, "/v2", listResp.GetRules()[0].GetPathRules()[0].GetPath())
}
