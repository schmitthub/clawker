package hostproxy

import (
	"context"
	"testing"
	"time"
)

func TestManagerProxyURL(t *testing.T) {
	m := NewManager()
	expected := "http://host.docker.internal:18374"
	if m.ProxyURL() != expected {
		t.Errorf("expected %q, got %q", expected, m.ProxyURL())
	}
}

func TestManagerPort(t *testing.T) {
	m := NewManager()
	if m.Port() != DefaultPort {
		t.Errorf("expected port %d, got %d", DefaultPort, m.Port())
	}
}

func TestManagerIsRunningInitially(t *testing.T) {
	m := NewManager()
	if m.IsRunning() {
		t.Error("expected manager to not be running initially")
	}
}

func TestManagerEnsureRunning(t *testing.T) {
	m := NewManager()

	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed, got error: %v", err)
	}

	if !m.IsRunning() {
		t.Error("expected manager to be running after EnsureRunning")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.Stop(ctx)
}

func TestManagerEnsureRunningIdempotent(t *testing.T) {
	m := NewManager()

	// First call
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected first EnsureRunning to succeed, got error: %v", err)
	}

	// Second call should also succeed (idempotent)
	err = m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected second EnsureRunning to succeed, got error: %v", err)
	}

	if !m.IsRunning() {
		t.Error("expected manager to still be running after second EnsureRunning")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.Stop(ctx)
}

func TestManagerStop(t *testing.T) {
	m := NewManager()

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed, got error: %v", err)
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
	m := NewManager()

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed, got error: %v", err)
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
	m := NewManager()

	// Start the server
	err := m.EnsureRunning()
	if err != nil {
		t.Fatalf("expected EnsureRunning to succeed, got error: %v", err)
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
