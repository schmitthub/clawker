package hostproxy

import (
	"testing"
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
