package loggertest_test

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger/loggertest"
)

func TestNew_CapturesOutput(t *testing.T) {
	tl := loggertest.New()

	tl.Info().Msg("hello world")

	output := tl.Output()
	if !strings.Contains(output, "hello world") {
		t.Errorf("Output() should contain logged message, got %q", output)
	}
}

func TestNew_Reset(t *testing.T) {
	tl := loggertest.New()

	tl.Info().Msg("first message")
	tl.Reset()

	if tl.Output() != "" {
		t.Error("Output() should be empty after Reset()")
	}

	tl.Info().Msg("second message")
	if !strings.Contains(tl.Output(), "second message") {
		t.Error("Output() should contain message logged after Reset()")
	}
}

func TestNewNop_DiscardsOutput(t *testing.T) {
	tl := loggertest.NewNop()

	tl.Info().Msg("should be discarded")

	if tl.Output() != "" {
		t.Errorf("NewNop().Output() should be empty, got %q", tl.Output())
	}
}

// Compile-time check: *TestLogger satisfies iostreams.Logger
var _ iostreams.Logger = (*loggertest.TestLogger)(nil)
