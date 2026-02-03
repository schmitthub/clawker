package hostproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

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
	m := NewManagerWithPort(12345)
	expected := "http://host.docker.internal:12345"
	if m.ProxyURL() != expected {
		t.Errorf("expected %q, got %q", expected, m.ProxyURL())
	}
}

func TestManagerPort(t *testing.T) {
	m := NewManagerWithPort(12345)
	if m.Port() != 12345 {
		t.Errorf("expected port %d, got %d", 12345, m.Port())
	}
}

func TestManagerIsRunningInitially(t *testing.T) {
	m := NewManagerWithPort(getFreeMgrPort(t))
	if m.IsRunning() {
		t.Error("expected manager to not be running initially")
	}
}

func TestManagerEnsureRunning(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})

	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed on port %d, got error: %v", port, err)
	}

	if !m.IsRunning() {
		t.Error("expected manager to be running after EnsureRunning")
	}
}

func TestManagerEnsureRunningIdempotent(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})

	// First call
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected first EnsureRunning to succeed on port %d, got error: %v", port, err)
	}

	// Second call should also succeed (idempotent)
	err = m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected second EnsureRunning to succeed, got error: %v", err)
	}

	if !m.IsRunning() {
		t.Error("expected manager to still be running after second EnsureRunning")
	}
}

func TestManagerStop(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed on port %d, got error: %v", port, err)
	}

	// Stop the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("expected Stop to succeed, got error: %v", err)
	}

	if m.IsRunning() {
		t.Error("expected manager to not be running after Stop")
	}
}

func TestManagerStopIsIdempotent(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed on port %d, got error: %v", port, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First stop
	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("expected first Stop to succeed, got error: %v", err)
	}

	// Second stop should also succeed (idempotent)
	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("expected second Stop to succeed, got error: %v", err)
	}
}

func TestManagerStopClearsServerReference(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed on port %d, got error: %v", port, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop the server
	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("expected Stop to succeed, got error: %v", err)
	}

	// Verify server reference is cleared by checking IsRunning
	if m.IsRunning() {
		t.Error("expected IsRunning to return false after Stop")
	}

	// Verify we can start again (server reference was cleared)
	err = m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed after Stop, got error: %v", err)
	}

	// Cleanup
	_ = m.Stop(ctx)
}

// TestManagerProxyUnavailableAfterStop demonstrates the core lifecycle problem:
// when the CLI process that started the manager exits (calls Stop), the proxy
// becomes unreachable, even though containers may still be running and trying
// to use it. This test simulates a container calling /open/url and /health
// after the manager has been stopped — both fail with connection refused.
func TestManagerProxyUnavailableAfterStop(t *testing.T) {
	port := getFreeMgrPort(t)
	m := NewManagerWithPort(port)

	// Start the proxy (simulates what happens during "clawker run @")
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("EnsureRunning failed: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}

	// Verify proxy is reachable (simulates container calling host-open successfully)
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("health check should succeed while running: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// CLI command exits — manager is stopped (simulates detach or command completion)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Container still running — tries to open a URL via host proxy
	// This is the bug: the proxy is gone but the container doesn't know
	_, err = client.Get(baseURL + "/health")
	if err == nil {
		t.Fatal("expected connection error after Stop, but request succeeded — proxy should be unreachable")
	}
	t.Logf("confirmed: proxy unreachable after Stop: %v", err)
}

// TestManagerSecondInstanceRecoversProxy shows that a second manager
// (e.g., from "clawker attach") can restart the proxy on the same port.
func TestManagerSecondInstanceRecoversProxy(t *testing.T) {
	port := getFreeMgrPort(t)

	// First CLI command starts the proxy
	m1 := NewManagerWithPort(port)
	err := m1.EnsureRunning()
	if err != nil {
		t.Fatalf("first EnsureRunning failed: %v", err)
	}

	// First CLI command exits
	ctx := context.Background()
	_ = m1.Stop(ctx)

	// Second CLI command starts a new manager on the same port
	m2 := NewManagerWithPort(port)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m2.Stop(ctx)
	})

	err = m2.EnsureRunning()
	if err != nil {
		t.Fatalf("second EnsureRunning failed: %v", err)
	}

	// Proxy is reachable again
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("health check should succeed after restart: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestManagerDefaultPort(t *testing.T) {
	m := NewManager()
	if m.Port() != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, m.Port())
	}
	expected := fmt.Sprintf("http://host.docker.internal:%d", DefaultPort)
	if m.ProxyURL() != expected {
		t.Errorf("expected %q, got %q", expected, m.ProxyURL())
	}
}
