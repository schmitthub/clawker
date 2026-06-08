package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWireInitExecutor_NilBus pins the CP-resilience contract: a
// regression that swaps the `if err != nil { return nil }` for
// `panic(err)` would crash CP and strand eBPF programs unsupervised,
// silently breaking the firewall enforcement boundary. The structured
// `event=<subsystem>_unavailable` log line is the only triage surface
// operators have once the wrapper degrades.
func TestWireInitExecutor_NilBus(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWriter(&buf)

	exec := wireInitExecutor(nil, log)

	require.Nil(t, exec, "nil bus must yield nil Executor (degrade), not crash")
	require.Contains(t, buf.String(), "agent_init_executor_unavailable",
		"degraded path must emit the structured event so operators can triage")
}

// TestLogHostIdentity pins the structured-event contract — the only
// place an operator can correlate downstream userStage EACCES with
// the CLI's CLAWKER_HOST_UID/GID env-drop. Renaming the event,
// dropping the emit, or swapping warn for debug must trip this test.
func TestLogHostIdentity(t *testing.T) {
	t.Run("happy path emits nothing", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		logHostIdentity(log,
			consts.HostIDResolution{Env: "CLAWKER_HOST_UID", Value: 1234, Fallback: false},
			consts.HostIDResolution{Env: "CLAWKER_HOST_GID", Value: 1234, Fallback: false},
		)
		require.Empty(t, buf.String(), "non-fallback resolutions must produce zero log lines")
	})

	t.Run("fallback emits structured warn per env", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		logHostIdentity(log,
			consts.HostIDResolution{Env: "CLAWKER_HOST_UID", Raw: "", Value: 1001, Fallback: true, Reason: "unset"},
			consts.HostIDResolution{Env: "CLAWKER_HOST_GID", Raw: "bad", Value: 1001, Fallback: true, Reason: "malformed", Err: assert.AnError},
		)
		// NewWriter is a zerolog JSON writer (one record per line).
		// Parse rather than substring-grep so a field rename or
		// formatter swap surfaces as a structured failure, not a
		// silently-passing test.
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		require.Len(t, lines, 2, "exactly one record per degraded env, got: %s", buf.String())

		want := []struct {
			env, reason string
			err         bool
		}{
			{"CLAWKER_HOST_UID", "unset", false},
			{"CLAWKER_HOST_GID", "malformed", true},
		}
		for i, line := range lines {
			var rec map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &rec),
				"record %d must be valid JSON: %q", i, line)
			require.Equal(t, "warn", rec["level"], "must use warn severity, not debug/info")
			require.Equal(t, "host_id_unavailable", rec["event"], "structured event name is the operator's triage anchor (env field disambiguates UID vs GID)")
			require.Equal(t, want[i].env, rec["env"])
			require.Equal(t, want[i].reason, rec["reason"])
			require.Equal(t, float64(1001), rec["fallback"], "fallback value must surface so operators know which UID userStage will drop to")
			if want[i].err {
				require.NotEmpty(t, rec["error"], "malformed reason must surface the underlying parse error")
			} else {
				_, hasErr := rec["error"]
				require.False(t, hasErr, "unset reason must not synthesize an error field")
			}
		}
	})
}

func TestParseOtlpEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantEndpoint string
		wantInsecure bool
	}{
		{"bare host_port defaults secure", "host.docker.internal:4319", "host.docker.internal:4319", false},
		{"https stays secure", "https://host.docker.internal:4319", "host.docker.internal:4319", false},
		{"explicit http opts in to plaintext", "http://collector:4317", "collector:4317", true},
		{"https with path strips path", "https://host:4319/v1/logs", "host:4319", false},
		{"http with path strips path", "http://host:4318/v1/logs", "host:4318", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, insecure := parseOtlpEndpoint(tc.raw)
			require.Equal(t, tc.wantEndpoint, endpoint)
			require.Equal(t, tc.wantInsecure, insecure)
		})
	}
}

func TestOtelOptionsFromEnv(t *testing.T) {
	t.Run("no env returns nil", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		require.Nil(t, otelOptionsFromEnv())
	})

	t.Run("logs endpoint precedence over generic", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://generic:4319")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "logs:4319", opts.Endpoint)
		require.False(t, opts.Insecure)
	})

	// CLI-root-direct cert env vars are deliberately ignored. The CP's
	// trusted-lane exporter takes its TLSConfig in-process from
	// internal/controlplane/otelcerts; allowing env-driven cert paths
	// would let an operator smuggle in a CLI-root-direct leaf, which
	// agent containers also hold — they could then forge
	// service.name=clawker-cp records on the trusted receiver.
	t.Run("client cert env vars are not consulted", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://host:4319")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "/c.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CLIENT_KEY", "/k.pem")
		t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "/ca.pem")

		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Empty(t, opts.ClientCertFile, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE must be ignored by the CP wiring")
		require.Empty(t, opts.ClientKeyFile, "OTEL_EXPORTER_OTLP_CLIENT_KEY must be ignored by the CP wiring")
		require.Empty(t, opts.CACertFile, "OTEL_EXPORTER_OTLP_CERTIFICATE must be ignored by the CP wiring")
		require.Nil(t, opts.TLSConfig, "TLSConfig is wired in-process by main, not from env")
	})

	t.Run("bare host_port defaults secure", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.prod.internal:4319")
		opts := otelOptionsFromEnv()
		require.NotNil(t, opts)
		require.Equal(t, "collector.prod.internal:4319", opts.Endpoint)
		require.False(t, opts.Insecure, "bare host:port must default to TLS")
	})
}

// TestDNSGCEscalator pins the "escalate once per crossing, reset on success"
// contract of the dns_cache GC degraded detector. The bug this guards against:
// a sweep that fails to reclaim must count toward the streak, and the
// dns_gc_degraded line must fire exactly once when the streak first reaches the
// threshold — not every tick after, and not at all if a success intervenes.
func TestDNSGCEscalator(t *testing.T) {
	t.Parallel()

	t.Run("fires once at threshold, then stays quiet", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 3}
		// Two failures: below threshold, no escalation.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		// Third consecutive failure crosses the threshold exactly once.
		assert.True(t, e.record(false))
		assert.Equal(t, 3, e.failures)
		// Further failures in the same streak must NOT re-fire.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
	})

	t.Run("a success resets the streak", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 3}
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		// Success before the threshold clears the streak.
		assert.False(t, e.record(true))
		assert.Equal(t, 0, e.failures)
		// The next failures start counting from zero again.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		assert.True(t, e.record(false))
	})

	t.Run("re-arms after a post-degraded success", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 2}
		assert.False(t, e.record(false))
		assert.True(t, e.record(false)) // first crossing
		assert.False(t, e.record(true)) // recover
		assert.False(t, e.record(false))
		assert.True(t, e.record(false)) // second crossing fires again
	})
}

// TestDNSGCSweep pins the per-sweep CP-resilience contract that the GC
// goroutine depends on: a panicking sweep must be recovered and counted as a
// failure (so the escalator can trip) rather than tearing down the loop and
// stranding the dns_cache map unsupervised, and a sweep where GarbageCollectDNS
// reports it could not reclaim must also count as a failure. A clean sweep is a
// success regardless of how many entries it cleared. The escalator test covers
// the boolean folding; this covers how each sweep outcome becomes that boolean.
func TestDNSGCSweep(t *testing.T) {
	t.Parallel()

	t.Run("clean sweep that reclaimed entries is success", func(t *testing.T) {
		t.Parallel()
		ok := dnsGCSweep(func() (int, error) { return 3, nil }, logger.Nop())
		require.True(t, ok)
	})

	t.Run("clean sweep that reclaimed nothing is still success", func(t *testing.T) {
		t.Parallel()
		// "swept nothing because nothing had expired" must not count as failure,
		// or a healthy idle CP would escalate to dns_gc_degraded.
		ok := dnsGCSweep(func() (int, error) { return 0, nil }, logger.Nop())
		require.True(t, ok)
	})

	t.Run("GarbageCollectDNS error is a failed sweep", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		ok := dnsGCSweep(func() (int, error) { return 0, errors.New("wedged") }, log)
		require.False(t, ok, "an unreclaimable sweep must count toward escalation")
		require.Contains(t, buf.String(), "dns_gc_error",
			"a failed reclaim must emit the structured event for triage")
	})

	t.Run("panic is recovered, counted as failure, and logged", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		// A panicking sweep must NOT propagate (which would kill the goroutine
		// and strand the dns_cache map) — the whole point of the recover.
		var ok bool
		require.NotPanics(t, func() {
			ok = dnsGCSweep(func() (int, error) { panic("boom") }, log)
		})
		require.False(t, ok, "a panicking sweep must count toward escalation")
		require.Contains(t, buf.String(), "dns_gc_panic",
			"a recovered panic must emit the structured event for triage")
	})
}
