package main

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
	p := newProgressReporter(&buf)
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
	p.Final("Running agent command...")

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
	p := newProgressReporter(&buf)

	p.Banner("first")
	p.Final("done")

	preStop := buf.String()
	// Anything after Final must NOT change the buffer.
	label := initStepLabel{Active: "after...", Done: "after done"}
	p.Banner("after-final-banner")
	p.StartStep(label)
	p.EndStep(label, true)
	p.Final("second-final")
	p.Stop()

	if buf.String() != preStop {
		t.Errorf("post-Final writes leaked into buffer:\nbefore: %q\nafter: %q", preStop, buf.String())
	}
}
