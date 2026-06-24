package controlplane

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWireExecutor_NilBus pins the CP-resilience contract: a
// regression that swaps the `if err != nil { return nil }` for
// `panic(err)` would crash CP and strand eBPF programs unsupervised,
// silently breaking the firewall enforcement boundary. The structured
// `event=<subsystem>_unavailable` log line is the only triage surface
// operators have once the wrapper degrades.
func TestWireExecutor_NilBus(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWriter(&buf)

	exec := wireExecutor(nil, nil, log)

	require.Nil(t, exec, "nil bus must yield nil Executor (degrade), not crash")
	require.Contains(t, buf.String(), "agent_executor_unavailable",
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

// TestRunDrainStage pins the CP §3.4 drain-resilience contract: a panicking
// teardown stage MUST be contained (recovered, not propagated) and reported as
// an error, so the linear drain sequence in run() falls through to
// ebpfMgr.FlushAll instead of unwinding the whole function and stranding eBPF.
// A clean stage runs its body and reports no error and no log noise. The
// structured event name is the only operator triage surface, so a rename or a
// dropped emit must trip this test.
func TestRunDrainStage(t *testing.T) {
	t.Parallel()

	t.Run("clean stage runs body, returns nil, logs nothing", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		ran := false
		err := runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
			ran = true
		})
		require.NoError(t, err, "a non-panicking stage must report no error")
		require.True(t, ran, "the stage body must execute")
		require.Empty(t, buf.String(), "a clean stage must emit no log line")
	})

	t.Run("panicking stage is contained and reported, not propagated", func(t *testing.T) {
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		var err error
		// The whole point of the recover: a panic in one teardown stage must
		// NOT unwind run() (which would skip FlushAll and strand eBPF).
		require.NotPanics(t, func() {
			err = runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
				panic("stack stop blew up")
			})
		})
		require.Error(t, err, "a panicking stage must surface as an error so the drain exits non-zero")
		require.Contains(t, err.Error(), "pre-flush teardown panic",
			"the error must name the stage so the aggregate drain error is triagable")
		require.Contains(t, buf.String(), "drain_preflush_panic",
			"a contained panic must emit the structured event — the only operator triage surface")
	})

	t.Run("a panicking stage still lets the caller reach a later stage", func(t *testing.T) {
		// This is the load-bearing FlushAll-still-runs guarantee, modeled as
		// the call site does it: two stages in sequence, the first panics,
		// the second (FlushAll's stand-in) must still run.
		log := logger.Nop()
		flushed := false
		_ = runDrainStage(log, "pre-flush teardown", "drain_preflush_panic", func() {
			panic("pre-flush blew up")
		})
		// Control returned here despite the panic — the caller proceeds to the
		// next stage exactly as drainCallbackBody does.
		_ = runDrainStage(log, "ebpf flush", "drain_ebpf_flush_panic", func() {
			flushed = true
		})
		require.True(t, flushed, "FlushAll-equivalent stage must run after a prior stage panics")
	})
}
