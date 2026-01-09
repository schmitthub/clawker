package logger

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestInit(t *testing.T) {
	// Test with debug disabled
	Init(false)

	if Log.GetLevel() != zerolog.InfoLevel {
		t.Errorf("Log level should be Info when debug=false, got %v", Log.GetLevel())
	}

	// Test with debug enabled
	Init(true)

	if Log.GetLevel() != zerolog.DebugLevel {
		t.Errorf("Log level should be Debug when debug=true, got %v", Log.GetLevel())
	}
}

func TestLogFunctions(t *testing.T) {
	Init(true) // Enable debug for testing

	// Test that log functions return non-nil events
	if Debug() == nil {
		t.Error("Debug() should return non-nil event")
	}
	if Info() == nil {
		t.Error("Info() should return non-nil event")
	}
	if Warn() == nil {
		t.Error("Warn() should return non-nil event")
	}
	if Error() == nil {
		t.Error("Error() should return non-nil event")
	}
	// Note: Don't test Fatal() as it would exit
}

func TestWithField(t *testing.T) {
	Init(false)

	logger := WithField("test_key", "test_value")

	// Verify the logger is not the zero value
	if logger.GetLevel() == zerolog.Disabled {
		t.Error("WithField should return a valid logger")
	}
}

func TestLoggerReinitialize(t *testing.T) {
	// Test that we can reinitialize the logger without issues
	Init(false)
	firstLevel := Log.GetLevel()

	Init(true)
	secondLevel := Log.GetLevel()

	if firstLevel == secondLevel {
		t.Error("Logger should have different levels after reinit with different debug flag")
	}

	// Reinit back to original
	Init(false)
	if Log.GetLevel() != firstLevel {
		t.Error("Logger should return to original level after reinit")
	}
}
