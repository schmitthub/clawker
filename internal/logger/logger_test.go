package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew_WritesToFile(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	l.Info().Msg("hello from test")
	l.Close()

	content, err := os.ReadFile(filepath.Join(dir, "clawker.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(content), "hello from test") {
		t.Errorf("log file missing message, got: %s", content)
	}
}

func TestNew_AllLevels(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	l.Debug().Msg("d")
	l.Info().Msg("i")
	l.Warn().Msg("w")
	l.Error().Msg("e")
	l.Close()

	content, err := os.ReadFile(filepath.Join(dir, "clawker.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if !strings.Contains(string(content), level) {
			t.Errorf("log file missing %s level", level)
		}
	}
}

func TestNew_EmptyLogsDir(t *testing.T) {
	_, err := New(Options{})
	if err == nil {
		t.Fatal("expected error for empty LogsDir")
	}
	if !strings.Contains(err.Error(), "LogsDir is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNew_InvalidPath(t *testing.T) {
	_, err := New(Options{LogsDir: "/dev/null/impossible/path"})
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestNew_Compress(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1, Compress: true})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer l.Close()

	if l.fw == nil {
		t.Fatal("file writer should not be nil")
	}
	if !l.fw.Compress {
		t.Error("Compress should be true")
	}
}

func TestNop(t *testing.T) {
	l := Nop()

	// Methods should not panic.
	l.Debug().Msg("discarded")
	l.Info().Msg("discarded")
	l.Warn().Msg("discarded")
	l.Error().Msg("discarded")

	if l.LogFilePath() != "" {
		t.Error("Nop logger should have empty log file path")
	}

	if err := l.Close(); err != nil {
		t.Errorf("Nop Close should not error: %v", err)
	}
}

func TestWith(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sub := l.With("project", "foo", "agent", "bar")
	sub.Info().Msg("with context")
	l.Close()

	content, err := os.ReadFile(filepath.Join(dir, "clawker.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "foo") || !strings.Contains(s, "bar") {
		t.Errorf("log should contain context fields, got: %s", s)
	}
}

func TestWith_OddArgs_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for odd args")
		}
	}()
	Nop().With("key")
}

func TestWith_NonStringKey_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for non-string key")
		}
	}()
	Nop().With(42, "value")
}

func TestLogFilePath(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer l.Close()

	want := filepath.Join(dir, "clawker.log")
	if got := l.LogFilePath(); got != want {
		t.Errorf("LogFilePath() = %q, want %q", got, want)
	}
}

func TestClose_Idempotent(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestOptions_Defaults(t *testing.T) {
	opts := &Options{}
	if opts.maxSizeMB() != 50 {
		t.Errorf("maxSizeMB default = %d, want 50", opts.maxSizeMB())
	}
	if opts.maxAgeDays() != 7 {
		t.Errorf("maxAgeDays default = %d, want 7", opts.maxAgeDays())
	}
	if opts.maxBackups() != 3 {
		t.Errorf("maxBackups default = %d, want 3", opts.maxBackups())
	}
}

func TestOptions_Custom(t *testing.T) {
	opts := &Options{MaxSizeMB: 20, MaxAgeDays: 14, MaxBackups: 5}
	if opts.maxSizeMB() != 20 {
		t.Errorf("maxSizeMB = %d, want 20", opts.maxSizeMB())
	}
	if opts.maxAgeDays() != 14 {
		t.Errorf("maxAgeDays = %d, want 14", opts.maxAgeDays())
	}
	if opts.maxBackups() != 5 {
		t.Errorf("maxBackups = %d, want 5", opts.maxBackups())
	}
}

func TestNew_NoStderrOutput(t *testing.T) {
	dir := t.TempDir()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		os.Stderr = oldStderr
		t.Fatalf("New failed: %v", err)
	}
	l.Info().Msg("should not appear on stderr")
	l.Warn().Msg("also not on stderr")
	l.Close()

	w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	r.Close()

	if n > 0 {
		t.Errorf("stderr should be empty, got: %q", string(buf[:n]))
	}
}

func TestNew_OtelFallback(t *testing.T) {
	dir := t.TempDir()

	// OTEL with unreachable endpoint — should not fail New().
	l, err := New(Options{
		LogsDir:   dir,
		MaxSizeMB: 1,
		Otel: &OtelOptions{
			Endpoint:       "127.0.0.1:19876",
			Insecure:       true,
			ExportInterval: 50 * time.Millisecond,
			MaxQueueSize:   10,
		},
	})
	if err != nil {
		t.Fatalf("New with OTEL should not fail: %v", err)
	}
	defer l.Close()

	l.Info().Msg("otel fallback test")
}
