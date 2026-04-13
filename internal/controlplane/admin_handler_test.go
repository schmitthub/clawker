package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/ebpf/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testCgroupPath is a real path under /sys/fs/cgroup/ that exists on Linux
// test hosts. CgroupID opens and stats this path to get its inode number,
// so it must be a real filesystem entry strictly under the cgroup root.
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

// newTestHandler creates an AdminHandler backed by the given mock.
func newTestHandler(mock *ebpfmocks.EBPFManagerMock) *AdminHandler {
	return NewAdminHandler(mock, logger.Nop())
}

// noopMock returns a mock with all methods set to no-op success.
func noopMock() *ebpfmocks.EBPFManagerMock {
	return &ebpfmocks.EBPFManagerMock{
		InstallFunc: func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error {
			return nil
		},
		RemoveFunc:     func(_ uint64) error { return nil },
		EnableFunc:     func(_ uint64) error { return nil },
		DisableFunc:    func(_ uint64) error { return nil },
		SyncRoutesFunc: func(_ []ebpf.Route) error { return nil },
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
// Install RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_Install_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Install(context.Background(), &adminv1.InstallRequest{
		ContainerId: "abc123",
		CgroupPath:  testCgroupPath,
		Config:      validContainerConfig(),
	})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id in response")
	}
	if len(mock.InstallCalls()) != 1 {
		t.Errorf("Install called %d times, want 1", len(mock.InstallCalls()))
	}
}

func TestAdminHandler_Install_NilConfig(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	_, err := h.Install(context.Background(), &adminv1.InstallRequest{
		CgroupPath: testCgroupPath,
		Config:     nil,
	})
	assertCode(t, err, codes.InvalidArgument)
	if len(mock.InstallCalls()) != 0 {
		t.Error("mock Install should not have been called")
	}
}

func TestAdminHandler_Install_EmptyCgroupPath(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	_, err := h.Install(context.Background(), &adminv1.InstallRequest{
		CgroupPath: "",
		Config:     validContainerConfig(),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestAdminHandler_Install_InvalidCgroupPath(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	_, err := h.Install(context.Background(), &adminv1.InstallRequest{
		CgroupPath: "/tmp/bad",
		Config:     validContainerConfig(),
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestAdminHandler_Install_EBPFError(t *testing.T) {
	mock := noopMock()
	mock.InstallFunc = func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error {
		return errors.New("bpf attach failed")
	}
	h := newTestHandler(mock)

	_, err := h.Install(context.Background(), &adminv1.InstallRequest{
		ContainerId: "abc123",
		CgroupPath:  testCgroupPath,
		Config:      validContainerConfig(),
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Remove RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_Remove_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Remove(context.Background(), &adminv1.RemoveRequest{
		CgroupPath: testCgroupPath,
	})
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id")
	}
	if len(mock.RemoveCalls()) != 1 {
		t.Errorf("Remove called %d times, want 1", len(mock.RemoveCalls()))
	}
}

func TestAdminHandler_Remove_EmptyCgroupPath(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	_, err := h.Remove(context.Background(), &adminv1.RemoveRequest{
		CgroupPath: "",
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestAdminHandler_Remove_EBPFError(t *testing.T) {
	mock := noopMock()
	mock.RemoveFunc = func(_ uint64) error {
		return errors.New("remove failed")
	}
	h := newTestHandler(mock)

	_, err := h.Remove(context.Background(), &adminv1.RemoveRequest{
		CgroupPath: testCgroupPath,
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Enable RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_Enable_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Enable(context.Background(), &adminv1.EnableRequest{
		CgroupPath: testCgroupPath,
	})
	if err != nil {
		t.Fatalf("Enable returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id")
	}
	if len(mock.EnableCalls()) != 1 {
		t.Errorf("Enable called %d times, want 1", len(mock.EnableCalls()))
	}
}

func TestAdminHandler_Enable_CancelsTimer(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	// Set up a bypass first to create a timer.
	_, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 3600, // very long so it won't fire during the test
	})
	if err != nil {
		t.Fatalf("Bypass setup returned error: %v", err)
	}

	// Verify the timer was created.
	h.bypassTimersMu.Lock()
	timerCount := len(h.bypassTimers)
	h.bypassTimersMu.Unlock()
	if timerCount != 1 {
		t.Fatalf("expected 1 bypass timer after Bypass, got %d", timerCount)
	}

	// Enable should cancel the timer.
	_, err = h.Enable(context.Background(), &adminv1.EnableRequest{
		CgroupPath: testCgroupPath,
	})
	if err != nil {
		t.Fatalf("Enable returned error: %v", err)
	}

	// Timer map should be empty.
	h.bypassTimersMu.Lock()
	timerCount = len(h.bypassTimers)
	h.bypassTimersMu.Unlock()
	if timerCount != 0 {
		t.Errorf("expected 0 bypass timers after Enable, got %d", timerCount)
	}
}

func TestAdminHandler_Enable_EBPFError(t *testing.T) {
	mock := noopMock()
	mock.EnableFunc = func(_ uint64) error {
		return errors.New("enable failed")
	}
	h := newTestHandler(mock)

	_, err := h.Enable(context.Background(), &adminv1.EnableRequest{
		CgroupPath: testCgroupPath,
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Disable RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_Disable_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Disable(context.Background(), &adminv1.DisableRequest{
		CgroupPath: testCgroupPath,
	})
	if err != nil {
		t.Fatalf("Disable returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id")
	}
	if len(mock.DisableCalls()) != 1 {
		t.Errorf("Disable called %d times, want 1", len(mock.DisableCalls()))
	}
}

func TestAdminHandler_Disable_EBPFError(t *testing.T) {
	mock := noopMock()
	mock.DisableFunc = func(_ uint64) error {
		return errors.New("disable failed")
	}
	h := newTestHandler(mock)

	_, err := h.Disable(context.Background(), &adminv1.DisableRequest{
		CgroupPath: testCgroupPath,
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Bypass RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_Bypass_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Bypass returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id")
	}
	// Bypass calls Disable under the hood.
	if len(mock.DisableCalls()) != 1 {
		t.Errorf("Disable called %d times, want 1", len(mock.DisableCalls()))
	}

	// Clean up: stop the timer.
	h.bypassTimersMu.Lock()
	for _, timer := range h.bypassTimers {
		timer.Stop()
	}
	h.bypassTimersMu.Unlock()
}

func TestAdminHandler_Bypass_DefaultTimeout(t *testing.T) {
	// When TimeoutSeconds is 0, the handler defaults to 30s.
	// We verify that Bypass succeeds (no panic, no error) with zero timeout.
	mock := noopMock()
	h := newTestHandler(mock)

	resp, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 0,
	})
	if err != nil {
		t.Fatalf("Bypass with zero timeout returned error: %v", err)
	}
	if resp.GetCgroupId() == 0 {
		t.Error("expected non-zero cgroup_id")
	}

	// Stop the timer to prevent the auto-enable goroutine from running.
	h.bypassTimersMu.Lock()
	for _, timer := range h.bypassTimers {
		timer.Stop()
	}
	h.bypassTimersMu.Unlock()
}

func TestAdminHandler_Bypass_CancelsPreviousTimer(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	// First bypass.
	_, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("first Bypass returned error: %v", err)
	}

	// Capture the first timer.
	cgroupID := uint64(0)
	h.bypassTimersMu.Lock()
	for id := range h.bypassTimers {
		cgroupID = id
	}
	firstTimer := h.bypassTimers[cgroupID]
	h.bypassTimersMu.Unlock()

	// Second bypass replaces the timer.
	_, err = h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("second Bypass returned error: %v", err)
	}

	h.bypassTimersMu.Lock()
	secondTimer := h.bypassTimers[cgroupID]
	h.bypassTimersMu.Unlock()

	// The timer object should have been replaced.
	if firstTimer == secondTimer {
		t.Error("second bypass should have replaced the timer, but got the same pointer")
	}

	// Disable was called twice (once per Bypass call).
	if len(mock.DisableCalls()) != 2 {
		t.Errorf("Disable called %d times, want 2", len(mock.DisableCalls()))
	}

	// Clean up.
	h.bypassTimersMu.Lock()
	for _, timer := range h.bypassTimers {
		timer.Stop()
	}
	h.bypassTimersMu.Unlock()
}

func TestAdminHandler_Bypass_EBPFError(t *testing.T) {
	mock := noopMock()
	mock.DisableFunc = func(_ uint64) error {
		return errors.New("disable failed")
	}
	h := newTestHandler(mock)

	_, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 60,
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// ResolveHostname RPC
// ---------------------------------------------------------------------------

func TestAdminHandler_ResolveHostname_Success(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return []string{"1.2.3.4", "5.6.7.8"}, nil
	}

	resp, err := h.ResolveHostname(context.Background(), &adminv1.ResolveHostnameRequest{
		Hostname: "example.com",
	})
	if err != nil {
		t.Fatalf("ResolveHostname returned error: %v", err)
	}
	if len(resp.GetAddresses()) != 2 {
		t.Errorf("expected 2 addresses, got %d", len(resp.GetAddresses()))
	}
	if resp.GetAddresses()[0] != "1.2.3.4" {
		t.Errorf("first address = %q, want %q", resp.GetAddresses()[0], "1.2.3.4")
	}
}

func TestAdminHandler_ResolveHostname_EmptyHostname(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)

	_, err := h.ResolveHostname(context.Background(), &adminv1.ResolveHostnameRequest{
		Hostname: "",
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestAdminHandler_ResolveHostname_DNSError(t *testing.T) {
	mock := noopMock()
	h := newTestHandler(mock)
	h.resolveHostFn = func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("dns lookup failed")
	}

	_, err := h.ResolveHostname(context.Background(), &adminv1.ResolveHostnameRequest{
		Hostname: "nonexistent.invalid",
	})
	assertCode(t, err, codes.Internal)
}

// ---------------------------------------------------------------------------
// Bypass timer auto-enable integration
// ---------------------------------------------------------------------------

func TestAdminHandler_Bypass_TimerAutoEnables(t *testing.T) {
	// Use a short timeout and verify the timer fires Enable automatically.
	enableCalled := make(chan uint64, 1)
	mock := noopMock()
	mock.EnableFunc = func(cgroupID uint64) error {
		enableCalled <- cgroupID
		return nil
	}
	h := newTestHandler(mock)

	_, err := h.Bypass(context.Background(), &adminv1.BypassRequest{
		CgroupPath:     testCgroupPath,
		TimeoutSeconds: 1, // 1 second
	})
	if err != nil {
		t.Fatalf("Bypass returned error: %v", err)
	}

	select {
	case <-enableCalled:
		// Timer fired and called Enable.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bypass timer to auto-enable")
	}
}
