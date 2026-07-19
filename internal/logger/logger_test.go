package logger

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
)

func TestNew_WritesToFile(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	l.Info().Msg("hello from test")
	l.Close(context.Background())

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
	l.Close(context.Background())

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
	defer l.Close(context.Background())

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

	if err := l.Close(context.Background()); err != nil {
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
	l.Close(context.Background())

	content, err := os.ReadFile(filepath.Join(dir, "clawker.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "foo") || !strings.Contains(s, "bar") {
		t.Errorf("log should contain context fields, got: %s", s)
	}
}

func TestWith_RepeatedKeyDedupes(t *testing.T) {
	var buf bytes.Buffer
	l := NewWriter(&buf)

	// Three layers each set "component" — the bug this guards against
	// stacked all three into one line (clawker-controlplane, agent.init,
	// agent.boot). The deduped logger keeps only the last value.
	sub := l.With("component", "clawker-controlplane").
		With("project", "demo").
		With("component", "agent.init").
		With("component", "agent.boot")
	sub.Error().Str("event", "agent_init_failed").Msg("boom")

	out := buf.String()
	if got := strings.Count(out, `"component"`); got != 1 {
		t.Fatalf("component key appears %d times, want 1; line: %s", got, out)
	}
	if !strings.Contains(out, `"component":"agent.boot"`) {
		t.Errorf("expected last component value to win; line: %s", out)
	}
	// Unrelated keys set once still survive the rebuild.
	if !strings.Contains(out, `"project":"demo"`) {
		t.Errorf("expected project field preserved; line: %s", out)
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
	defer l.Close(context.Background())

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

	if err := l.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := l.Close(context.Background()); err != nil {
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
	l.Close(context.Background())

	w.Close()
	os.Stderr = oldStderr

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	r.Close()

	if n > 0 {
		t.Errorf("stderr should be empty, got: %q", string(buf[:n]))
	}
}

// TestNew_EchoStdout_MirrorsRecords pins the contract that
// containerized daemons (clawkercp) get every log record copied to
// os.Stdout so `docker logs <container>` is non-empty. A regression
// that drops the os.Stdout sink would silently brick operator
// triage; no other test exercises this property.
func TestNew_EchoStdout_MirrorsRecords(t *testing.T) {
	dir := t.TempDir()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1, EchoStdout: true})
	if err != nil {
		os.Stdout = oldStdout
		t.Fatalf("New failed: %v", err)
	}
	l.Info().Str("event", "ready").Msg("clawkercp ready")
	l.Close(context.Background())

	w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	r.Close()

	got := string(buf[:n])
	if !strings.Contains(got, "clawkercp ready") {
		t.Errorf("EchoStdout=true must mirror records to os.Stdout; got: %q", got)
	}
	if !strings.Contains(got, `"event":"ready"`) {
		t.Errorf("EchoStdout must preserve structured fields; got: %q", got)
	}
}

// TestNew_EchoStdoutOff_KeepsStdoutQuiet pins the host-side CLI
// contract: stdout is reserved for command output and must not
// receive logger records when EchoStdout is unset. Default Options
// must remain a host-safe default.
func TestNew_EchoStdoutOff_KeepsStdoutQuiet(t *testing.T) {
	dir := t.TempDir()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	l, err := New(Options{LogsDir: dir, MaxSizeMB: 1})
	if err != nil {
		os.Stdout = oldStdout
		t.Fatalf("New failed: %v", err)
	}
	l.Info().Msg("should not appear on stdout")
	l.Close(context.Background())

	w.Close()
	os.Stdout = oldStdout

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	r.Close()

	if n > 0 {
		t.Errorf("stdout should be empty without EchoStdout, got: %q", string(buf[:n]))
	}
}

func TestNew_OtelFallback(t *testing.T) {
	dir := t.TempDir()

	// OTEL with unreachable endpoint — should not fail New().
	l, err := New(Options{
		LogsDir:   dir,
		MaxSizeMB: 1,
		Otel: &OtelOptions{
			Endpoint:       consts.Localhost + ":19876",
			Insecure:       true,
			Timeout:        50 * time.Millisecond,
			ExportInterval: 50 * time.Millisecond,
			MaxQueueSize:   10,
		},
	})
	if err != nil {
		t.Fatalf("New with OTEL should not fail: %v", err)
	}
	defer l.Close(context.Background())

	l.Info().Msg("otel fallback test")
}

// TestClose_CanceledContext_ReturnsPromptly pins the exit-lag fix: Close must
// honor a canceled caller context so the final OTEL flush unwinds immediately
// instead of blocking on an unreachable collector. The CLI relies on exactly
// this — it cancels the flush context when the command returns, so the deferred
// Close never does a blocking final export. Before the fix Close ignored the
// caller context entirely (hardcoded 5s context.Background()), so cancellation
// could not stop the ~5s block; the blocking shutdown path had no coverage.
func TestClose_CanceledContext_ReturnsPromptly(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{
		LogsDir:   dir,
		MaxSizeMB: 1,
		Otel: &OtelOptions{
			// Nothing listens here: with a live context the final export
			// would retry against the unreachable endpoint until the export
			// timeout. A canceled context must short-circuit that.
			Endpoint: consts.Localhost + ":19876",
			Insecure: true,
		},
	})
	if err != nil {
		t.Fatalf("New with OTEL should not fail: %v", err)
	}
	if l.provider == nil {
		t.Fatal("OTEL provider must be wired for this test to exercise the flush path")
	}

	l.Info().Msg("buffered record awaiting export")

	// Mirror the CLI: the flush context is already canceled by the time Close
	// runs (loggerCancel() fires before the deferred Close).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_ = l.Close(ctx)
	elapsed := time.Since(start)

	// A canceled context unwinds Shutdown's export immediately. The regressed
	// behavior ignored the context and blocked ~5s on the export timeout. The
	// 2s threshold sits well clear of both, so the test is fast and not flaky.
	if elapsed >= 2*time.Second {
		t.Errorf("Close blocked %v on a canceled context; the flush must unwind immediately", elapsed)
	}
}

// TestClose_LiveContext_BoundedByExportTimeout pins the daemon shutdown path,
// the symmetric partner to the canceled-context test. clawkercp passes a live
// (never-canceled) base context to Close, so a final flush against an
// unreachable collector must be bounded by the exporter's own export timeout
// (OtelOptions.Timeout), not ride the OTLP retry backoff (~1m MaxElapsedTime).
// Without WithTimeout wired from OtelOptions.Timeout in newOtelProvider, this
// blocks for tens of seconds — so the assertion goes red if that wiring
// regresses.
func TestClose_LiveContext_BoundedByExportTimeout(t *testing.T) {
	dir := t.TempDir()

	l, err := New(Options{
		LogsDir:   dir,
		MaxSizeMB: 1,
		Otel: &OtelOptions{
			// Unreachable endpoint + a short export timeout: the final flush
			// must give up at the timeout, not ride the retry backoff.
			Endpoint: consts.Localhost + ":19876",
			Insecure: true,
			Timeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("New with OTEL should not fail: %v", err)
	}
	if l.provider == nil {
		t.Fatal("OTEL provider must be wired for this test to exercise the flush path")
	}

	l.Info().Msg("buffered record awaiting export")

	// Live context — never canceled, mirroring clawkercp's deferred
	// log.Close(ctx) on the base background context.
	start := time.Now()
	err = l.Close(context.Background())
	elapsed := time.Since(start)

	// The 100ms export timeout bounds the final flush. The 2s threshold sits
	// well clear of it yet far under the ~1m retry MaxElapsedTime the unbounded
	// path would block for — fast and not flaky.
	if elapsed >= 2*time.Second {
		t.Errorf("Close blocked %v on a live context; OtelOptions.Timeout must bound the flush", elapsed)
	}

	// The export failure against the unreachable collector is the true outcome
	// of the close and must surface — Close reports whatever Shutdown returns
	// and no longer swallows context errors. Goes red if the suppression is
	// reintroduced (which would hide a real daemon-path export failure).
	if err == nil {
		t.Error(
			"Close returned nil on a live context with an unreachable collector; the export-timeout failure must surface",
		)
	}
}

// TestOtelOptionsFromEnv pins the secure-by-default OTLP-endpoint
// resolution contract: the helper resolves only the endpoint + plaintext
// flag from env (the logs-signal override winning over the generic var),
// defaults to TLS for bare host:port, opts in to plaintext ONLY for an
// explicit http:// scheme, and NEVER honors env-driven cert paths (those
// would let an operator smuggle a CLI-root-direct leaf onto the trusted
// lane). Moved out of internal/controlplane when the helper became a
// logger-package concern.
func TestOtelOptionsFromEnv(t *testing.T) {
	t.Run("no env returns nil", func(t *testing.T) {
		t.Setenv(consts.EnvOTLPLogsEndpoint, "")
		t.Setenv(consts.EnvOTLPEndpoint, "")
		if got := OtelOptionsFromEnv(); got != nil {
			t.Fatalf("expected nil with no endpoint configured, got %+v", got)
		}
	})

	t.Run("logs endpoint precedence over generic", func(t *testing.T) {
		t.Setenv(consts.EnvOTLPLogsEndpoint, "https://logs:4319")
		t.Setenv(consts.EnvOTLPEndpoint, "https://generic:4319")
		opts := OtelOptionsFromEnv()
		if opts == nil {
			t.Fatal("expected non-nil opts")
		}
		if opts.Endpoint != "logs:4319" {
			t.Errorf("endpoint: got %q, want %q", opts.Endpoint, "logs:4319")
		}
		if opts.Insecure {
			t.Error("https endpoint must resolve secure (Insecure=false)")
		}
	})

	t.Run("explicit http opts in to plaintext", func(t *testing.T) {
		t.Setenv(consts.EnvOTLPLogsEndpoint, "")
		t.Setenv(consts.EnvOTLPEndpoint, "http://collector:4317")
		opts := OtelOptionsFromEnv()
		if opts == nil {
			t.Fatal("expected non-nil opts")
		}
		if opts.Endpoint != "collector:4317" {
			t.Errorf("endpoint: got %q, want %q", opts.Endpoint, "collector:4317")
		}
		if !opts.Insecure {
			t.Error("explicit http:// must opt in to plaintext (Insecure=true)")
		}
	})

	t.Run("bare host_port defaults secure", func(t *testing.T) {
		t.Setenv(consts.EnvOTLPLogsEndpoint, "")
		t.Setenv(consts.EnvOTLPEndpoint, "collector.prod.internal:4319")
		opts := OtelOptionsFromEnv()
		if opts == nil {
			t.Fatal("expected non-nil opts")
		}
		if opts.Endpoint != "collector.prod.internal:4319" {
			t.Errorf("endpoint: got %q, want %q", opts.Endpoint, "collector.prod.internal:4319")
		}
		if opts.Insecure {
			t.Error("bare host:port must default to TLS (Insecure=false)")
		}
	})

	// CLI-root-direct cert env vars are deliberately ignored. The CP's
	// trusted-lane exporter takes its TLSConfig in-process from
	// internal/controlplane/otelcerts; honoring env-driven cert paths
	// would let an operator smuggle in a CLI-root-direct leaf, which
	// agent containers also hold — they could then forge
	// service.name=clawkercp records on the trusted receiver.
	t.Run("client cert env vars are not consulted", func(t *testing.T) {
		t.Setenv(consts.EnvOTLPLogsEndpoint, "https://host:4319")
		t.Setenv(consts.EnvOTLPEndpoint, "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/k.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/ca.pem")

		opts := OtelOptionsFromEnv()
		if opts == nil {
			t.Fatal("expected non-nil opts")
		}
		if opts.ClientCertFile != "" {
			t.Error("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE must be ignored")
		}
		if opts.ClientKeyFile != "" {
			t.Error("OTEL_EXPORTER_OTLP_CLIENT_KEY must be ignored")
		}
		if opts.CACertFile != "" {
			t.Error("OTEL_EXPORTER_OTLP_CERTIFICATE must be ignored")
		}
		if opts.TLSConfig != nil {
			t.Error("TLSConfig is wired in-process by the caller, not from env")
		}
	})
}
