//go:build linux

// Tests exercise Handler against ebpf.CgroupID, which stats a real path
// under /sys/fs/cgroup/ to read the cgroup v2 inode. That path only
// exists on Linux, so the suite is Linux-gated to keep macOS commits
// unblocked. CI runs on Linux.

package firewall

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testCgroupPath is a real path under /sys/fs/cgroup/ that exists on
// Linux test hosts. CgroupID opens and stats this path to get its inode
// number, so it must be a real filesystem entry strictly under the
// cgroup root.
const testCgroupPath = "/sys/fs/cgroup/cgroup.procs"

// validContainerConfig returns a proto ContainerConfig with valid IPs.
func validContainerConfig() *adminv1.ContainerConfig {
	return &adminv1.ContainerConfig{
		EnvoyIp:       "172.20.0.2",
		CorednsIp:     "172.20.0.3",
		GatewayIp:     "172.20.0.1",
		Cidr:          "172.20.0.0/16",
		HostProxyIp:   "172.20.0.4",
		HostProxyPort: 8080,
		EgressPort:    10000,
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
	mu                              sync.Mutex
	ensureRunningCalls, reloadCalls int
	ensureErr                       error
	statusResult                    Status
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
	st := f.statusResult
	return &st, nil
}

// newTestHandler builds a Handler with the given EBPF mock and resolver,
// a fake stack, and no rule store. Tests that exercise rule-store paths
// build their own Handler with a real store via testenv.
func newTestHandler(mock *ebpfmocks.EBPFManagerMock, resolver ContainerResolver) *Handler {
	if resolver == nil {
		resolver = nopResolver
	}
	return NewHandler(HandlerDeps{
		EBPF:     mock,
		Stack:    &fakeStack{},
		Resolver: resolver,
		Log:      logger.Nop(),
	})
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
		Config:      validContainerConfig(),
	})
	if err != nil {
		t.Fatalf("FirewallEnable returned error: %v", err)
	}
	if got := len(mock.InstallCalls()); got != 1 {
		t.Fatalf("Install called %d times, want 1", got)
	}
}

func TestHandler_FirewallEnable_EmptyContainerID(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{Config: validContainerConfig()})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallEnable_NilConfig(t *testing.T) {
	h := newTestHandler(noopMock(), nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{ContainerId: "ctr-1"})
	assertCode(t, err, codes.InvalidArgument)
}

func TestHandler_FirewallEnable_ContainerGone_FailedPrecondition(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	h := newTestHandler(noopMock(), resolver)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-gone",
		Config:      validContainerConfig(),
	})
	assertCode(t, err, codes.FailedPrecondition)
}

func TestHandler_FirewallEnable_DriftDetected_UsesFreshID(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock, nil)

	freshID, err := ebpf.CgroupID(testCgroupPath)
	if err != nil {
		t.Fatalf("CgroupID: %v", err)
	}

	// Pre-populate stored cgroup ID with a stale value.
	h.cgroupIDMu.Lock()
	h.storedCgroupID["ctr-drift"] = freshID + 1
	h.cgroupIDMu.Unlock()

	_, err = h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-drift",
		Config:      validContainerConfig(),
	})
	if err != nil {
		t.Fatalf("FirewallEnable returned error: %v", err)
	}

	calls := mock.InstallCalls()
	if len(calls) != 1 {
		t.Fatalf("Install called %d times, want 1", len(calls))
	}
	if calls[0].CgroupID != freshID {
		t.Errorf("Install called with cgroupID=%d, want fresh %d (drift correction failed)", calls[0].CgroupID, freshID)
	}

	h.cgroupIDMu.Lock()
	stored := h.storedCgroupID["ctr-drift"]
	h.cgroupIDMu.Unlock()
	if stored != freshID {
		t.Errorf("storedCgroupID after Enable = %d, want fresh %d", stored, freshID)
	}
}

func TestHandler_FirewallEnable_EBPFError_PropagatesInternal(t *testing.T) {
	mock := noopMock()
	mock.InstallFunc = func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error {
		return errors.New("install failed")
	}
	h := newTestHandler(mock, nil)
	_, err := h.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{
		ContainerId: "ctr-err", Config: validContainerConfig(),
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
		ContainerId: cid, Config: validContainerConfig(),
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

func TestHandler_FirewallInit_DelegatesToStack(t *testing.T) {
	stack := &fakeStack{statusResult: Status{EnvoyIP: "1.2.3.4", CoreDNSIP: "1.2.3.5", NetworkID: "n1"}}
	h := NewHandler(HandlerDeps{
		EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(),
	})
	resp, err := h.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{})
	if err != nil {
		t.Fatalf("FirewallInit returned error: %v", err)
	}
	if resp.GetEnvoyIp() != "1.2.3.4" || resp.GetCorednsIp() != "1.2.3.5" || resp.GetNetworkId() != "n1" {
		t.Errorf("response mismatch: %+v", resp)
	}
	if stack.ensureRunningCalls != 1 {
		t.Errorf("EnsureRunning called %d times, want 1", stack.ensureRunningCalls)
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

func TestHandler_FirewallReload_DelegatesToStack(t *testing.T) {
	stack := &fakeStack{}
	h := NewHandler(HandlerDeps{
		EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(),
	})
	resp, err := h.FirewallReload(context.Background(), &adminv1.FirewallReloadRequest{})
	if err != nil {
		t.Fatalf("FirewallReload returned error: %v", err)
	}
	if !resp.GetStackRestarted() {
		t.Error("stack_restarted should be true")
	}
	if stack.reloadCalls != 1 {
		t.Errorf("Reload called %d times, want 1", stack.reloadCalls)
	}
}

func TestHandler_FirewallStatus_PropagatesStackFields(t *testing.T) {
	stack := &fakeStack{statusResult: Status{
		Running: true, EnvoyHealth: true, CoreDNSHealth: true,
		RuleCount: 7, EnvoyIP: "10.0.0.2", CoreDNSIP: "10.0.0.3", NetworkID: "net-1",
	}}
	h := NewHandler(HandlerDeps{
		EBPF: noopMock(), Stack: stack, Resolver: nopResolver, Log: logger.Nop(),
	})
	resp, err := h.FirewallStatus(context.Background(), &adminv1.FirewallStatusRequest{})
	if err != nil {
		t.Fatalf("FirewallStatus returned error: %v", err)
	}
	if !resp.GetRunning() || resp.GetRuleCount() != 7 || resp.GetEnvoyIp() != "10.0.0.2" {
		t.Errorf("response mismatch: %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// resolveBypassCgroupID branches (drift helper)
// ---------------------------------------------------------------------------

func TestResolveBypassCgroupID_EmptyContainerID_FallsBack(t *testing.T) {
	entry := &bypassEntry{cgroupID: 99999}
	got := resolveBypassCgroupID(entry, nopResolver, logger.Nop())
	if got != 99999 {
		t.Errorf("expected fallback to stored cgroup ID 99999, got %d", got)
	}
}

func TestResolveBypassCgroupID_DockerAPIError_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, errors.New("docker unavailable")
	}
	entry := &bypassEntry{containerID: "ctr-docker-err", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, logger.Nop())
	if got != 12345 {
		t.Errorf("expected fallback to stored cgroup ID 12345, got %d", got)
	}
}

func TestResolveBypassCgroupID_ContainerGone_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "", "", false, nil
	}
	entry := &bypassEntry{containerID: "ctr-gone", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, logger.Nop())
	if got != 12345 {
		t.Errorf("expected fallback to stored cgroup ID 12345, got %d", got)
	}
}

func TestResolveBypassCgroupID_ContainerAlive_ReturnsFreshID(t *testing.T) {
	expectedID, err := ebpf.CgroupID(testCgroupPath)
	if err != nil {
		t.Fatalf("CgroupID(%s) failed: %v", testCgroupPath, err)
	}
	entry := &bypassEntry{containerID: "ctr-alive", cgroupID: expectedID}
	got := resolveBypassCgroupID(entry, nopResolver, logger.Nop())
	if got != expectedID {
		t.Errorf("expected fresh cgroup ID %d, got %d", expectedID, got)
	}
}

func TestResolveBypassCgroupID_DriftDetected_UsesFresh(t *testing.T) {
	expectedID, err := ebpf.CgroupID(testCgroupPath)
	if err != nil {
		t.Fatalf("CgroupID(%s) failed: %v", testCgroupPath, err)
	}
	entry := &bypassEntry{containerID: "ctr-drift", cgroupID: expectedID + 1}
	got := resolveBypassCgroupID(entry, nopResolver, logger.Nop())
	if got != expectedID {
		t.Errorf("expected fresh cgroup ID %d (drift from stored %d), got %d", expectedID, entry.cgroupID, got)
	}
}

func TestResolveBypassCgroupID_CgroupStatFails_FallsBack(t *testing.T) {
	resolver := func(_ context.Context, _ string) (string, string, bool, error) {
		return "ref", "/sys/fs/cgroup/nonexistent/path", true, nil
	}
	entry := &bypassEntry{containerID: "ctr-stat-fail", cgroupID: 12345}
	got := resolveBypassCgroupID(entry, resolver, logger.Nop())
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

// TestProtoRulesRoundTrip pins the field map between adminv1.EgressRule
// /PathRule and config.EgressRule/PathRule. A future field drift between
// the two struct sets will surface here as a missing assertion target,
// not as silent data loss across the gRPC boundary.
func TestProtoRulesRoundTrip(t *testing.T) {
	in := []*adminv1.EgressRule{
		{
			Dst: "api.example.com", Proto: "tls", Port: 443, Action: "allow",
			PathRules:   []*adminv1.PathRule{{Path: "/v1", Action: "allow"}},
			PathDefault: "deny",
		},
	}
	cfgs := protoRulesToConfig(in)
	if len(cfgs) != 1 || cfgs[0].Dst != "api.example.com" || cfgs[0].PathDefault != "deny" {
		t.Fatalf("protoRulesToConfig mismatch: %+v", cfgs)
	}
	if len(cfgs[0].PathRules) != 1 || cfgs[0].PathRules[0].Path != "/v1" {
		t.Errorf("PathRules mismatch: %+v", cfgs[0].PathRules)
	}
	out := configRulesToProto(cfgs)
	if len(out) != 1 || out[0].GetDst() != "api.example.com" {
		t.Fatalf("configRulesToProto mismatch: %+v", out)
	}
}
