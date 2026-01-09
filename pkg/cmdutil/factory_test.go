package cmdutil

import (
	"testing"
)

func TestNew(t *testing.T) {
	f := New("1.0.0", "abc123")

	if f.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", f.Version)
	}
	if f.Commit != "abc123" {
		t.Errorf("expected commit 'abc123', got '%s'", f.Commit)
	}
	if f.WorkDir != "" {
		t.Errorf("expected empty WorkDir, got '%s'", f.WorkDir)
	}
	if f.Debug != false {
		t.Errorf("expected Debug false, got true")
	}
}

func TestFactory_ConfigLoader(t *testing.T) {
	f := New("1.0.0", "abc123")
	f.WorkDir = "/tmp/test"

	loader1 := f.ConfigLoader()
	loader2 := f.ConfigLoader()

	// Should return same instance (lazy initialization)
	if loader1 != loader2 {
		t.Error("ConfigLoader should return the same instance on subsequent calls")
	}
}

func TestFactory_ResetConfig(t *testing.T) {
	f := New("1.0.0", "abc123")
	f.WorkDir = "/tmp/nonexistent"

	// First call will fail (no config file)
	_, err1 := f.Config()
	if err1 == nil {
		t.Skip("Config unexpectedly succeeded, skipping reset test")
	}

	// Reset and verify error is cleared
	f.ResetConfig()

	// After reset, configData and configErr should be nil
	if f.configData != nil {
		t.Error("configData should be nil after reset")
	}
	if f.configErr != nil {
		t.Error("configErr should be nil after reset")
	}
}
