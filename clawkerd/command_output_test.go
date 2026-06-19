package clawkerd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
)

// shStage builds a single-stage `sh -c <script>` pipeline.
func shStage(script string) *clawkerdv1.ShellCommand {
	return &clawkerdv1.ShellCommand{
		Stages: []*clawkerdv1.PipeStage{{Argv: []string{"/bin/sh", "-c", script}}},
	}
}

// TestRunShellCommand_CombinedOutputSingleStream proves a command's
// stdout and stderr surface as one ordered OutputChunk stream (2>&1): a
// command that writes to both fds arrives as a single combined stream in
// write order, never two separate channels.
func TestRunShellCommand_CombinedOutputSingleStream(t *testing.T) {
	s, _ := newTestSession()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Single-threaded sh writes A (stdout) then B (stderr) in program
	// order; merged into one stream they arrive as "AB".
	runUntilDone(t, ctx, s, shStage("printf A; printf B >&2"), "merge-1")

	var out strings.Builder
	for _, r := range drainAll(s) {
		if c := r.GetOutput(); c != nil {
			out.Write(c.Data)
		}
	}
	assert.Equal(t, "AB", out.String(),
		"stdout+stderr must arrive as one combined OutputChunk stream in write order")
}

// TestRunShellCommand_PrintOutputEcho proves print_output gates the
// local TTY echo: the command's combined output reaches the boot console
// live when set, and nothing when unset — regardless of exit status.
func TestRunShellCommand_PrintOutputEcho(t *testing.T) {
	cases := []struct {
		name        string
		printOutput bool
		wantEcho    bool
	}{
		{"print_output true echoes", true, true},
		{"print_output false silent", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSession()
			var console bytes.Buffer
			s.progress = NewProgressReporter(&console)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			sc := shStage("printf hook-output")
			sc.PrintOutput = tc.printOutput
			runUntilDone(t, ctx, s, sc, "echo-1")

			if tc.wantEcho {
				assert.Contains(t, console.String(), "hook-output",
					"print_output=true must echo captured output to the console")
			} else {
				assert.NotContains(t, console.String(), "hook-output",
					"print_output=false must not echo to the console")
			}
		})
	}
}

// TestRunShellCommand_ExitOnNonZeroRequestsExit proves exit_on_non_zero
// mirrors the command's exit code to the daemon shutdown seam only on a
// non-zero exit, and only when the flag is set. clawkerd holds no policy
// — absent the flag it never requests exit.
func TestRunShellCommand_ExitOnNonZeroRequestsExit(t *testing.T) {
	cases := []struct {
		name          string
		script        string
		exitOnNonZero bool
		wantCode      int // -1 = requestExit must NOT be called
	}{
		{"flag + non-zero mirrors code", "exit 3", true, 3},
		{"flag + zero does not exit", "exit 0", true, -1},
		{"no flag + non-zero does not exit", "exit 3", false, -1},
		// Signal-killed final stage has no clean exit code (exitCodeOf
		// returns -1); the mirror clamps to a generic non-zero status
		// rather than a nonsensical negative.
		{"flag + signal clamps to generic non-zero", "kill -KILL $$", true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestSession()
			gotCode := -1
			s.requestExit = func(code int) { gotCode = code }

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			sc := shStage(tc.script)
			sc.ExitOnNonZero = tc.exitOnNonZero
			runUntilDone(t, ctx, s, sc, "exit-1")

			assert.Equal(t, tc.wantCode, gotCode)
		})
	}
}

// TestSettleInitStep_RendersExitCode proves the init progress line
// reflects the command's exit code: a non-zero Done renders the red ✗
// (failed) line, a zero Done renders the green ✓ — fixing the bug where
// any Done rendered success.
func TestSettleInitStep_RendersExitCode(t *testing.T) {
	// Valid init command_id: init- + 13-char container prefix + step-idx.
	const commandID = "init-abcdef012345-post-init-5"

	t.Run("non-zero renders failed", func(t *testing.T) {
		s, _ := newTestSession()
		var console bytes.Buffer
		s.progress = NewProgressReporter(&console)
		s.settleInitStep(&clawkerdv1.Response{
			CommandId: commandID,
			Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 1}},
		})
		assert.Contains(t, console.String(), "(failed)",
			"non-zero Done must render the failed init line")
	})

	t.Run("zero renders success", func(t *testing.T) {
		s, _ := newTestSession()
		var console bytes.Buffer
		s.progress = NewProgressReporter(&console)
		s.settleInitStep(&clawkerdv1.Response{
			CommandId: commandID,
			Payload:   &clawkerdv1.Response_Done{Done: &clawkerdv1.Done{FinalExitCode: 0}},
		})
		assert.NotContains(t, console.String(), "(failed)",
			"zero Done must not render the failed init line")
		require.Contains(t, console.String(), "✓",
			"zero Done must render the success marker")
	})
}
