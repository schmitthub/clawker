package hostproxy

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// newMockConfigWithPort creates a mock config with a custom manager port.
func newMockConfigWithPort(t *testing.T, port int) config.Config {
	t.Helper()
	return configmocks.NewFromString(fmt.Sprintf(`host_proxy: { manager: { port: %d }, daemon: { port: %d } }`, port, port))
}

// getFreeMgrPort returns an available TCP port for manager tests.
func getFreeMgrPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestManagerProxyURL(t *testing.T) {
	cfg := newMockConfigWithPort(t, 12345)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	expected := "http://host.docker.internal:12345"
	if m.ProxyURL() != expected {
		t.Errorf("expected %q, got %q", expected, m.ProxyURL())
	}
}

func TestManagerPort(t *testing.T) {
	cfg := newMockConfigWithPort(t, 12345)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m.Port() != 12345 {
		t.Errorf("expected port %d, got %d", 12345, m.Port())
	}
}

func TestManagerIsRunningInitially(t *testing.T) {
	cfg := newMockConfigWithPort(t, getFreeMgrPort(t))
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m.IsRunning() {
		t.Error("expected manager to not be running initially")
	}
}

func TestManagerDefaultPort(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if m.Port() != 18374 {
		t.Errorf("expected default port 18374, got %d", m.Port())
	}
	expected := "http://host.docker.internal:18374"
	if m.ProxyURL() != expected {
		t.Errorf("expected %q, got %q", expected, m.ProxyURL())
	}
}

func TestManagerInvalidPort(t *testing.T) {
	cfg := configmocks.NewFromString(`host_proxy: { manager: { port: 0 } }`)
	_, err := NewManager(cfg)
	if err == nil {
		t.Fatal("expected error for invalid port 0")
	}
}

func TestManagerStopIsNoOp(t *testing.T) {
	cfg := newMockConfigWithPort(t, getFreeMgrPort(t))
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	// Stop should not panic when called on a non-running manager
	m.Stop()
}

// TestIsDaemonRunningWithStalePIDFile tests that stale PID files are handled correctly.
func TestIsDaemonRunningWithStalePIDFile(t *testing.T) {
	// Create a temp PID file with a non-existent PID
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hostproxy.pid")

	// Write a PID that doesn't exist (use a very high PID)
	if err := os.WriteFile(pidFile, []byte("999999999"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	if IsDaemonRunning(pidFile) {
		t.Error("expected IsDaemonRunning to return false for stale PID file")
	}
}

// TestIsDaemonRunningWithMissingPIDFile tests handling of missing PID file.
func TestIsDaemonRunningWithMissingPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	if IsDaemonRunning(pidFile) {
		t.Error("expected IsDaemonRunning to return false for missing PID file")
	}
}

// TestGetDaemonPIDWithMissingFile tests GetDaemonPID with no PID file.
func TestGetDaemonPIDWithMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	pid := GetDaemonPID(pidFile)
	if pid != 0 {
		t.Errorf("expected PID 0 for missing file, got %d", pid)
	}
}

// TestStopDaemonWithMissingFile tests StopDaemon with no running daemon.
func TestStopDaemonWithMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	err := StopDaemon(pidFile)
	if err != nil {
		t.Errorf("expected no error for missing PID file, got: %v", err)
	}
}

// TestStopDaemonWithStalePIDFile tests StopDaemon cleanup of stale PID file.
func TestStopDaemonWithStalePIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "hostproxy.pid")

	// Write a PID that doesn't exist
	if err := os.WriteFile(pidFile, []byte("999999999"), 0644); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	err := StopDaemon(pidFile)
	if err != nil {
		t.Errorf("expected no error for stale PID, got: %v", err)
	}

	// Verify PID file was cleaned up
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("expected stale PID file to be removed")
	}
}

// TestWriteAndReadPIDFile tests PID file operations.
func TestWriteAndReadPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "test.pid")

	// Write PID file
	if err := writePIDFile(pidFile); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	// Read it back
	pid, err := readPIDFile(pidFile)
	if err != nil {
		t.Fatalf("failed to read PID file: %v", err)
	}

	// Should be current process PID
	if pid != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), pid)
	}

	// Clean up
	removePIDFile(pidFile)
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("expected PID file to be removed")
	}
}

// TestIsProcessAlive tests process liveness detection.
func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !isProcessAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}

	// Non-existent PID should not be alive
	if isProcessAlive(999999999) {
		t.Error("expected non-existent PID to not be alive")
	}
}

// TestManagerHealthCheck tests the health check functionality.
func TestManagerHealthCheck(t *testing.T) {
	port := getFreeMgrPort(t)
	cfg := newMockConfigWithPort(t, port)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Health check should fail when nothing is running
	if err := m.healthCheck(); err == nil {
		t.Error("expected health check to fail when server not running")
	}

	// Start a mock server
	server := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok","service":"clawker-host-proxy"}`))
		}),
	}
	go server.ListenAndServe()
	defer server.Close()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Health check should pass now
	if err := m.healthCheck(); err != nil {
		t.Errorf("expected health check to pass, got error: %v", err)
	}
}

// TestManagerIsPortInUse tests port detection.
func TestManagerIsPortInUse(t *testing.T) {
	port := getFreeMgrPort(t)
	cfg := newMockConfigWithPort(t, port)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Port should not be in use initially
	if m.isPortInUse() {
		t.Error("expected port to not be in use initially")
	}

	// Start a mock clawker host proxy
	server := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok","service":"clawker-host-proxy"}`))
		}),
	}
	go server.ListenAndServe()
	defer server.Close()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Port should be in use now
	if !m.isPortInUse() {
		t.Error("expected port to be in use")
	}
}

// TestManagerIsPortInUseWithWrongService tests that we detect non-clawker services.
func TestManagerIsPortInUseWithWrongService(t *testing.T) {
	port := getFreeMgrPort(t)
	cfg := newMockConfigWithPort(t, port)
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Start a server that returns a different service identifier
	server := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok","service":"some-other-service"}`))
		}),
	}
	go server.ListenAndServe()
	defer server.Close()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Port should NOT be considered in use by clawker
	if m.isPortInUse() {
		t.Error("expected isPortInUse to return false for non-clawker service")
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port    int
		wantErr bool
	}{
		{0, true},
		{-1, true},
		{65536, true},
		{1, false},
		{18374, false},
		{65535, false},
	}
	for _, tt := range tests {
		err := validatePort(tt.port, "test")
		if (err != nil) != tt.wantErr {
			t.Errorf("validatePort(%d): got err=%v, wantErr=%v", tt.port, err, tt.wantErr)
		}
	}
}
