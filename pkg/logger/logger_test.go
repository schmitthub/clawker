package logger

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSetInteractiveMode(t *testing.T) {
	// Test that SetInteractiveMode can be toggled without panic
	SetInteractiveMode(true)
	SetInteractiveMode(false)
	SetInteractiveMode(true)
	SetInteractiveMode(false)
}

func TestInfoSuppressedInInteractiveMode(t *testing.T) {
	// Initialize logger with Info level (not debug)
	Init(false)
	defer SetInteractiveMode(false) // Cleanup

	// Without interactive mode, Info() should return a normal event
	SetInteractiveMode(false)
	event := Info()
	if event == nil {
		t.Error("Info() should return non-nil event when not in interactive mode")
	}

	// With interactive mode, Info() should return a nop event (nil)
	// because console logs are suppressed
	SetInteractiveMode(true)
	event = Info()
	if event != nil {
		t.Error("Info() should return nil event in interactive mode (suppressed on console)")
	}
}

func TestInfoNotSuppressedInDebugMode(t *testing.T) {
	// Initialize logger with Debug level
	Init(true)
	defer SetInteractiveMode(false) // Cleanup

	// With interactive mode AND debug level, Info() should still log
	SetInteractiveMode(true)
	event := Info()
	if event == nil {
		t.Error("Info() should return non-nil event in debug mode even with interactive mode")
	}
}

func TestWarnSuppressedInInteractiveMode(t *testing.T) {
	// Initialize logger with Info level (not debug)
	Init(false)
	defer SetInteractiveMode(false) // Cleanup

	// Without interactive mode, Warn() should return a normal event
	SetInteractiveMode(false)
	event := Warn()
	if event == nil {
		t.Error("Warn() should return non-nil event when not in interactive mode")
	}

	// With interactive mode, Warn() should return a nop event (nil)
	// because console logs are suppressed
	SetInteractiveMode(true)
	event = Warn()
	if event != nil {
		t.Error("Warn() should return nil event in interactive mode (suppressed on console)")
	}
}

func TestErrorSuppressedInInteractiveMode(t *testing.T) {
	// Initialize logger with Info level (not debug)
	Init(false)
	defer SetInteractiveMode(false) // Cleanup

	// Without interactive mode, Error() should return a normal event
	SetInteractiveMode(false)
	event := Error()
	if event == nil {
		t.Error("Error() should return non-nil event when not in interactive mode")
	}

	// With interactive mode, Error() should return a nop event (nil)
	// because console logs are suppressed
	SetInteractiveMode(true)
	event = Error()
	if event != nil {
		t.Error("Error() should return nil event in interactive mode (suppressed on console)")
	}
}

func TestLoggingConfigDefaults(t *testing.T) {
	// Test nil FileEnabled defaults to true
	cfg := &LoggingConfig{}
	if !cfg.IsFileEnabled() {
		t.Error("IsFileEnabled should default to true when nil")
	}

	// Test explicit false
	falseVal := false
	cfg.FileEnabled = &falseVal
	if cfg.IsFileEnabled() {
		t.Error("IsFileEnabled should return false when explicitly set")
	}

	// Test explicit true
	trueVal := true
	cfg.FileEnabled = &trueVal
	if !cfg.IsFileEnabled() {
		t.Error("IsFileEnabled should return true when explicitly set")
	}

	// Test zero values default correctly
	cfg = &LoggingConfig{}
	if cfg.GetMaxSizeMB() != 50 {
		t.Errorf("GetMaxSizeMB should default to 50, got %d", cfg.GetMaxSizeMB())
	}
	if cfg.GetMaxAgeDays() != 7 {
		t.Errorf("GetMaxAgeDays should default to 7, got %d", cfg.GetMaxAgeDays())
	}
	if cfg.GetMaxBackups() != 3 {
		t.Errorf("GetMaxBackups should default to 3, got %d", cfg.GetMaxBackups())
	}

	// Test custom values
	cfg = &LoggingConfig{
		MaxSizeMB:  20,
		MaxAgeDays: 14,
		MaxBackups: 5,
	}
	if cfg.GetMaxSizeMB() != 20 {
		t.Errorf("GetMaxSizeMB should return 20, got %d", cfg.GetMaxSizeMB())
	}
	if cfg.GetMaxAgeDays() != 14 {
		t.Errorf("GetMaxAgeDays should return 14, got %d", cfg.GetMaxAgeDays())
	}
	if cfg.GetMaxBackups() != 5 {
		t.Errorf("GetMaxBackups should return 5, got %d", cfg.GetMaxBackups())
	}
}

func TestInitWithFile(t *testing.T) {
	// Create a temporary directory for log files
	tmpDir, err := os.MkdirTemp("", "clawker-logger-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{
		MaxSizeMB:  1,
		MaxAgeDays: 1,
		MaxBackups: 1,
	}

	// Initialize with file logging
	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}

	// Verify log file path is set
	logPath := GetLogFilePath()
	if logPath == "" {
		t.Error("GetLogFilePath should return non-empty path after InitWithFile")
	}

	expectedPath := filepath.Join(tmpDir, "clawker.log")
	if logPath != expectedPath {
		t.Errorf("GetLogFilePath = %q, want %q", logPath, expectedPath)
	}

	// Write a log message
	Info().Msg("test log message")

	// Close the file writer
	err = CloseFileWriter()
	if err != nil {
		t.Errorf("CloseFileWriter failed: %v", err)
	}

	// Verify log file was created
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("Log file should have been created")
	}

	// Verify log file has content
	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if len(content) == 0 {
		t.Error("Log file should have content")
	}
	if !strings.Contains(string(content), "test log message") {
		t.Error("Log file should contain the test message")
	}
}

func TestInitWithFileDisabled(t *testing.T) {
	// Reset any previous file writer
	fileWriter = nil

	// Test with file logging disabled
	falseVal := false
	cfg := &LoggingConfig{
		FileEnabled: &falseVal,
	}

	err := InitWithFile(false, "/some/path", cfg)
	if err != nil {
		t.Fatalf("InitWithFile with disabled file logging should not fail: %v", err)
	}

	// Verify no file writer is set
	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when file logging is disabled")
	}
}

func TestInitWithFileEmptyDir(t *testing.T) {
	// Reset any previous file writer
	fileWriter = nil

	cfg := &LoggingConfig{}

	// Empty logs dir should result in console-only logging
	err := InitWithFile(false, "", cfg)
	if err != nil {
		t.Fatalf("InitWithFile with empty dir should not fail: %v", err)
	}

	// Verify no file writer is set
	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when logsDir is empty")
	}
}

func TestInitWithFileNilConfig(t *testing.T) {
	// Reset any previous file writer
	fileWriter = nil

	// Nil config should result in console-only logging
	err := InitWithFile(false, "/some/path", nil)
	if err != nil {
		t.Fatalf("InitWithFile with nil config should not fail: %v", err)
	}

	// Verify no file writer is set
	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when config is nil")
	}
}

func TestFileLoggingInInteractiveMode(t *testing.T) {
	// Create a temporary directory for log files
	tmpDir, err := os.MkdirTemp("", "clawker-logger-interactive-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{
		MaxSizeMB:  1,
		MaxAgeDays: 1,
		MaxBackups: 1,
	}

	// Initialize with file logging
	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer SetInteractiveMode(false)

	// Enable interactive mode
	SetInteractiveMode(true)

	// Write a log message - should go to file even though console is suppressed
	Info().Msg("interactive mode test message")

	// Flush by closing
	CloseFileWriter()

	// Verify log file has the message
	logPath := filepath.Join(tmpDir, "clawker.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "interactive mode test message") {
		t.Error("Log file should contain message even when in interactive mode")
	}
}

func TestCloseFileWriterWhenNil(t *testing.T) {
	// Reset file writer
	fileWriter = nil

	// Should not panic or error
	err := CloseFileWriter()
	if err != nil {
		t.Errorf("CloseFileWriter should return nil when fileWriter is nil, got: %v", err)
	}
}

func TestSetContext(t *testing.T) {
	Init(false)
	defer ClearContext()

	// Set context
	SetContext("myproject", "myagent")

	ctx := getContext()
	if ctx.Project != "myproject" {
		t.Errorf("Project = %q, want %q", ctx.Project, "myproject")
	}
	if ctx.Agent != "myagent" {
		t.Errorf("Agent = %q, want %q", ctx.Agent, "myagent")
	}

	// Clear context
	ClearContext()
	ctx = getContext()
	if ctx.Project != "" || ctx.Agent != "" {
		t.Error("ClearContext should reset both fields")
	}
}

func TestSetContextPartial(t *testing.T) {
	Init(false)
	defer ClearContext()

	// Set only project
	SetContext("onlyproject", "")
	ctx := getContext()
	if ctx.Project != "onlyproject" {
		t.Errorf("Project = %q, want %q", ctx.Project, "onlyproject")
	}
	if ctx.Agent != "" {
		t.Errorf("Agent should be empty, got %q", ctx.Agent)
	}

	// Set only agent
	SetContext("", "onlyagent")
	ctx = getContext()
	if ctx.Project != "" {
		t.Errorf("Project should be empty, got %q", ctx.Project)
	}
	if ctx.Agent != "onlyagent" {
		t.Errorf("Agent = %q, want %q", ctx.Agent, "onlyagent")
	}
}

func TestContextInFileLog(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "logger-context-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer ClearContext()

	SetContext("testproj", "testagent")
	Info().Msg("context test")

	CloseFileWriter()

	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "testproj") {
		t.Error("Log should contain project name")
	}
	if !strings.Contains(string(content), "testagent") {
		t.Error("Log should contain agent name")
	}
}

func TestContextInFileLogPartial(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "logger-context-partial-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer ClearContext()

	// Set only project, no agent
	SetContext("projonly", "")
	Info().Msg("partial context test")

	CloseFileWriter()

	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "projonly") {
		t.Error("Log should contain project name")
	}
	// Agent should not appear when empty
	if strings.Contains(string(content), `"agent"`) {
		t.Error("Log should not contain agent field when empty")
	}
}

func TestContextNotInLogWhenEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "logger-context-empty-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer ClearContext()

	// Ensure context is clear
	ClearContext()
	Info().Msg("no context test")

	CloseFileWriter()

	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	// Neither project nor agent should appear when empty
	if strings.Contains(string(content), `"project"`) {
		t.Error("Log should not contain project field when empty")
	}
	if strings.Contains(string(content), `"agent"`) {
		t.Error("Log should not contain agent field when empty")
	}
}

// resetLoggerState resets all global logger state for test isolation
func resetLoggerState() {
	fileWriter = nil
	fileOnlyLog = zerolog.Nop()
	interactiveMode = false
	logContext = logContextData{}
}

func TestAllSuppressedLevelsGoToFileInInteractiveMode(t *testing.T) {
	resetLoggerState()

	tmpDir, err := os.MkdirTemp("", "clawker-logger-all-levels-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{
		MaxSizeMB:  1,
		MaxAgeDays: 1,
		MaxBackups: 1,
	}

	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer SetInteractiveMode(false)

	SetInteractiveMode(true)

	// Test all suppressed levels go to file
	Info().Msg("info in interactive mode")
	Warn().Msg("warn in interactive mode")
	Error().Msg("error in interactive mode")

	CloseFileWriter()

	logPath := filepath.Join(tmpDir, "clawker.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	// Verify all messages appear in file
	if !strings.Contains(string(content), "info in interactive mode") {
		t.Error("Log file should contain Info message in interactive mode")
	}
	if !strings.Contains(string(content), "warn in interactive mode") {
		t.Error("Log file should contain Warn message in interactive mode")
	}
	if !strings.Contains(string(content), "error in interactive mode") {
		t.Error("Log file should contain Error message in interactive mode")
	}
}

func TestDebugNotSuppressedInInteractiveMode(t *testing.T) {
	resetLoggerState()

	tmpDir, err := os.MkdirTemp("", "clawker-logger-debug-interactive-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{
		MaxSizeMB:  1,
		MaxAgeDays: 1,
		MaxBackups: 1,
	}

	// Initialize with debug enabled
	err = InitWithFile(true, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer SetInteractiveMode(false)

	SetInteractiveMode(true)

	// Debug should still write to file in interactive mode
	Debug().Msg("debug in interactive mode")

	CloseFileWriter()

	logPath := filepath.Join(tmpDir, "clawker.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "debug in interactive mode") {
		t.Error("Log file should contain Debug message in interactive mode")
	}
}

func TestCloseFileWriterResetsState(t *testing.T) {
	resetLoggerState()

	tmpDir, err := os.MkdirTemp("", "clawker-logger-close-reset-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &LoggingConfig{MaxSizeMB: 1}

	err = InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}

	// Verify file writer is set
	if GetLogFilePath() == "" {
		t.Error("GetLogFilePath should return path after InitWithFile")
	}

	// Close should reset state
	err = CloseFileWriter()
	if err != nil {
		t.Errorf("CloseFileWriter failed: %v", err)
	}

	// Verify state is reset
	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty after CloseFileWriter")
	}

	// Double close should not error
	err = CloseFileWriter()
	if err != nil {
		t.Errorf("Double CloseFileWriter should not error: %v", err)
	}
}

func TestInitWithFilePermissionError(t *testing.T) {
	resetLoggerState()

	// Use a path that will fail (e.g., under /dev/null or invalid path)
	// On most systems, trying to create a directory under a regular file fails
	err := InitWithFile(false, "/dev/null/deeply/nested/path/that/fails", &LoggingConfig{})
	if err == nil {
		// Some systems might handle this differently, so don't fail if it doesn't error
		// but verify no file writer was created
		if GetLogFilePath() != "" {
			t.Error("GetLogFilePath should return empty for invalid path")
		}
		return
	}
	// Error was returned as expected
	if !strings.Contains(err.Error(), "failed to create logs directory") {
		t.Errorf("Error should mention directory creation, got: %v", err)
	}
}
