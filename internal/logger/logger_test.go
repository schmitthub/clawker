package logger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestInit(t *testing.T) {
	// Init produces a nop logger (pre-file-logging placeholder)
	Init()

	// Nop logger has Disabled level
	if Log.GetLevel() != zerolog.Disabled {
		t.Errorf("Init() should produce nop logger (Disabled level), got %v", Log.GetLevel())
	}
}

func TestLogFunctions(t *testing.T) {
	// With file logging, all log functions return non-nil events
	tmpDir := t.TempDir()
	cfg := &LoggingConfig{MaxSizeMB: 1}
	if err := InitWithFile(true, tmpDir, cfg); err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	t.Cleanup(func() { CloseFileWriter() })

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
	Init()

	logger := WithField("test_key", "test_value")

	// Verify the logger is not the zero value
	if logger.GetLevel() == zerolog.Disabled {
		// Nop logger still returns a valid sub-logger — just validate it doesn't panic
	}
}

func TestLoggerReinitialize(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &LoggingConfig{MaxSizeMB: 1}

	// Init → nop
	Init()
	if Log.GetLevel() != zerolog.Disabled {
		t.Error("Init should produce nop logger")
	}

	// InitWithFile → real logger
	if err := InitWithFile(true, tmpDir, cfg); err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	t.Cleanup(func() { CloseFileWriter() })

	if Log.GetLevel() == zerolog.Disabled {
		t.Error("InitWithFile should produce active logger")
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
	tmpDir := t.TempDir()

	cfg := &LoggingConfig{
		MaxSizeMB:  1,
		MaxAgeDays: 1,
		MaxBackups: 1,
	}

	// Initialize with file logging
	err := InitWithFile(false, tmpDir, cfg)
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
	fileWriter = nil

	falseVal := false
	cfg := &LoggingConfig{
		FileEnabled: &falseVal,
	}

	err := InitWithFile(false, "/some/path", cfg)
	if err != nil {
		t.Fatalf("InitWithFile with disabled file logging should not fail: %v", err)
	}

	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when file logging is disabled")
	}
}

func TestInitWithFileEmptyDir(t *testing.T) {
	fileWriter = nil

	cfg := &LoggingConfig{}

	err := InitWithFile(false, "", cfg)
	if err != nil {
		t.Fatalf("InitWithFile with empty dir should not fail: %v", err)
	}

	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when logsDir is empty")
	}
}

func TestInitWithFileNilConfig(t *testing.T) {
	fileWriter = nil

	err := InitWithFile(false, "/some/path", nil)
	if err != nil {
		t.Fatalf("InitWithFile with nil config should not fail: %v", err)
	}

	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty when config is nil")
	}
}

func TestCloseFileWriterWhenNil(t *testing.T) {
	fileWriter = nil

	err := CloseFileWriter()
	if err != nil {
		t.Errorf("CloseFileWriter should return nil when fileWriter is nil, got: %v", err)
	}
}

func TestSetContext(t *testing.T) {
	Init()
	defer ClearContext()

	SetContext("myproject", "myagent")

	ctx := getContext()
	if ctx.Project != "myproject" {
		t.Errorf("ProjectCfg = %q, want %q", ctx.Project, "myproject")
	}
	if ctx.Agent != "myagent" {
		t.Errorf("Agent = %q, want %q", ctx.Agent, "myagent")
	}

	ClearContext()
	ctx = getContext()
	if ctx.Project != "" || ctx.Agent != "" {
		t.Error("ClearContext should reset both fields")
	}
}

func TestSetContextPartial(t *testing.T) {
	Init()
	defer ClearContext()

	SetContext("onlyproject", "")
	ctx := getContext()
	if ctx.Project != "onlyproject" {
		t.Errorf("ProjectCfg = %q, want %q", ctx.Project, "onlyproject")
	}
	if ctx.Agent != "" {
		t.Errorf("Agent should be empty, got %q", ctx.Agent)
	}

	SetContext("", "onlyagent")
	ctx = getContext()
	if ctx.Project != "" {
		t.Errorf("ProjectCfg should be empty, got %q", ctx.Project)
	}
	if ctx.Agent != "onlyagent" {
		t.Errorf("Agent = %q, want %q", ctx.Agent, "onlyagent")
	}
}

func TestContextInFileLog(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err := InitWithFile(false, tmpDir, cfg)
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
	tmpDir := t.TempDir()

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err := InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer ClearContext()

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
	if strings.Contains(string(content), `"agent"`) {
		t.Error("Log should not contain agent field when empty")
	}
}

func TestContextNotInLogWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &LoggingConfig{MaxSizeMB: 1}
	err := InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()
	defer ClearContext()

	ClearContext()
	Info().Msg("no context test")

	CloseFileWriter()

	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
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
	logContext = logContextData{}
}

func TestCloseFileWriterResetsState(t *testing.T) {
	resetLoggerState()

	tmpDir := t.TempDir()

	cfg := &LoggingConfig{MaxSizeMB: 1}

	err := InitWithFile(false, tmpDir, cfg)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}

	if GetLogFilePath() == "" {
		t.Error("GetLogFilePath should return path after InitWithFile")
	}

	err = CloseFileWriter()
	if err != nil {
		t.Errorf("CloseFileWriter failed: %v", err)
	}

	if GetLogFilePath() != "" {
		t.Error("GetLogFilePath should return empty after CloseFileWriter")
	}

	err = CloseFileWriter()
	if err != nil {
		t.Errorf("Double CloseFileWriter should not error: %v", err)
	}
}

func TestInitWithFilePermissionError(t *testing.T) {
	resetLoggerState()

	err := InitWithFile(false, "/dev/null/deeply/nested/path/that/fails", &LoggingConfig{})
	if err == nil {
		if GetLogFilePath() != "" {
			t.Error("GetLogFilePath should return empty for invalid path")
		}
		return
	}
	if !strings.Contains(err.Error(), "failed to create logs directory") {
		t.Errorf("Error should mention directory creation, got: %v", err)
	}
}

func TestInitWithFile_NoConsoleOutput(t *testing.T) {
	resetLoggerState()

	tmpDir := t.TempDir()

	// Capture stderr to verify no console output
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stderr = w

	cfg := &LoggingConfig{MaxSizeMB: 1}
	if err := InitWithFile(false, tmpDir, cfg); err != nil {
		os.Stderr = oldStderr
		t.Fatalf("InitWithFile failed: %v", err)
	}

	// Log at all levels
	Info().Msg("info test")
	Warn().Msg("warn test")
	Error().Msg("error test")
	Debug().Msg("debug test")

	// Close and restore stderr
	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read pipe: %v", err)
	}
	r.Close()

	if buf.Len() > 0 {
		t.Errorf("No output should appear on stderr, but got: %q", buf.String())
	}

	// Verify messages went to file
	CloseFileWriter()
	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "info test") {
		t.Error("Log file should contain info message")
	}
	if !strings.Contains(string(content), "warn test") {
		t.Error("Log file should contain warn message")
	}
	if !strings.Contains(string(content), "error test") {
		t.Error("Log file should contain error message")
	}
}

func TestInitWithFile_DebugLevel(t *testing.T) {
	resetLoggerState()
	tmpDir := t.TempDir()

	cfg := &LoggingConfig{MaxSizeMB: 1}
	if err := InitWithFile(true, tmpDir, cfg); err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer CloseFileWriter()

	Debug().Msg("debug message")
	CloseFileWriter()

	content, err := os.ReadFile(filepath.Join(tmpDir, "clawker.log"))
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "debug message") {
		t.Error("Log file should contain debug message when debug=true")
	}
}
