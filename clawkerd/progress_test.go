package clawkerd

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseInitStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id         string
		wantOk     bool
		wantActive string
		wantDone   string
	}{
		{
			id:         "init-abcdef012345-config-1",
			wantOk:     true,
			wantActive: "Seeding agent config...",
			wantDone:   "Agent config seeded",
		},
		{
			id:         "init-abcdef012345-git-credentials-3",
			wantOk:     true,
			wantActive: "Configuring git credentials...",
			wantDone:   "Git credentials configured",
		},
		{
			id:         "init-abcdef012345-agent-ready-6",
			wantOk:     true,
			wantActive: "Running agent command...",
			wantDone:   "Agent command running",
		},
		{
			// Unknown step — fall through to raw step name in both forms
			// (so a CP that adds a plan entry without a label still
			// renders something sensible).
			id:         "init-abcdef012345-future-step-7",
			wantOk:     true,
			wantActive: "future-step...",
			wantDone:   "future-step",
		},
		{
			// Non-init command IDs don't match.
			id:     "shell-cmd-42",
			wantOk: false,
		},
		{
			// Truncated ID — too short to extract a step.
			id:     "init-",
			wantOk: false,
		},
	}
	for _, tc := range cases {
		got, ok := parseInitStep(tc.id)
		if ok != tc.wantOk {
			t.Errorf("parseInitStep(%q) ok = %v, want %v", tc.id, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if got.Active != tc.wantActive {
			t.Errorf("parseInitStep(%q).Active = %q, want %q", tc.id, got.Active, tc.wantActive)
		}
		if got.Done != tc.wantDone {
			t.Errorf("parseInitStep(%q).Done = %q, want %q", tc.id, got.Done, tc.wantDone)
		}
	}
}

// TestProgressReporter_LinearOutput exercises the happy path: each
// step emits a "starting" line then a completion line, with the
// failure form annotated when EndStep is called with ok=false.
func TestProgressReporter_LinearOutput(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := NewProgressReporter(&buf)
	if p.isTTY {
		t.Fatalf("buffer must not register as TTY")
	}

	p.Banner("Starting Clawker agent...")
	cfgLabel := initStepLabel{Active: "Seeding agent config...", Done: "Agent config seeded"}
	gitLabel := initStepLabel{Active: "Configuring git...", Done: "Git configured"}
	p.StartStep(cfgLabel)
	p.EndStep(cfgLabel, true)
	p.StartStep(gitLabel)
	p.EndStep(gitLabel, false)
	p.Final()

	out := buf.String()
	for _, want := range []string{
		"[info] Starting Clawker agent...",
		"  Seeding agent config...",
		"  ✓ Agent config seeded",
		"  Configuring git...",
		"  ✗ Configuring git... (failed)",
		"[info] Running agent command...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %q", want, out)
		}
	}
}

// TestProgressReporter_StopMutesFurtherWrites verifies the post-Stop
// guarantee. Once Stop or Final fires, every subsequent method call
// must be a no-op so we never interleave output with the spawned
// user CMD's TTY stream.
func TestProgressReporter_StopMutesFurtherWrites(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := NewProgressReporter(&buf)

	p.Banner("first")
	p.Final()

	preStop := buf.String()
	// Anything after Final must NOT change the buffer.
	label := initStepLabel{Active: "after...", Done: "after done"}
	p.Banner("after-final-banner")
	p.StartStep(label)
	p.EndStep(label, true)
	p.Final()
	p.Stop()

	if buf.String() != preStop {
		t.Errorf("post-Final writes leaked into buffer:\nbefore: %q\nafter: %q", preStop, buf.String())
	}
}

// TestProgressReporter_StopBeforeFinalSuppressesBanner verifies the
// Stop-then-Final ordering. Stop is "quiet cleanup, no banner" — once
// it claims the muted slot, a subsequent Final must NOT emit a closing
// banner. Final's mutex-guarded `stopped` check is the gate.
func TestProgressReporter_StopBeforeFinalSuppressesBanner(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := NewProgressReporter(&buf)

	p.Stop()
	p.Final()

	if got := buf.String(); got != "" {
		t.Errorf("Stop-then-Final emitted output, want none: %q", got)
	}
}

// TestProgressReporter_NilReceiverSafe verifies the documented
// nil-safe contract — every method must no-op on a nil receiver so
// test sessions and degraded wiring paths can leave progress unset
// without crashing PID 1.
func TestProgressReporter_NilReceiverSafe(t *testing.T) {
	t.Parallel()
	var p *progressReporter
	label := initStepLabel{Active: "x...", Done: "x done"}

	p.Banner("nope")
	p.StartStep(label)
	p.EndStep(label, true)
	p.EndStep(label, false)
	p.Final()
	p.Stop()
}
