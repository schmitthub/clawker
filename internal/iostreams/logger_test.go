package iostreams

import (
	"testing"

	"github.com/rs/zerolog"
)

// Compile-time check: *zerolog.Logger satisfies the Logger interface.
var _ Logger = (*zerolog.Logger)(nil)

func TestZerologLogger_SatisfiesInterface(t *testing.T) {
	// Runtime verification that *zerolog.Logger satisfies Logger.
	// The compile-time check above is the real guarantee; this test
	// documents the expectation explicitly.
	zl := zerolog.New(nil) // writes nowhere but returns non-nil events
	var l Logger = &zl

	// Verify the interface methods are callable and return non-nil events
	if l.Debug() == nil {
		t.Error("Debug() should return non-nil event")
	}
	if l.Info() == nil {
		t.Error("Info() should return non-nil event")
	}
	if l.Warn() == nil {
		t.Error("Warn() should return non-nil event")
	}
	if l.Error() == nil {
		t.Error("Error() should return non-nil event")
	}
}
