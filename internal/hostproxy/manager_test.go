package hostproxy

import (
	"context"
	"fmt"
	"net"
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
