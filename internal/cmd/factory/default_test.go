package factory

import (
	"testing"
)

func TestNew(t *testing.T) {
	f := New("1.0.0")

	if f.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", f.Version)
	}
	if f.IOStreams == nil {
		t.Error("expected IOStreams to be non-nil")
	}
	if f.TUI == nil {
		t.Error("expected TUI to be non-nil")
	}
}
