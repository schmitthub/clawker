package docker

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/term"
)

func TestResetVisualStateUnlocked_Terminal(t *testing.T) {
	// Create a pipe to capture stdout output
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// Create PTYHandler with pipe as stdout
	// We need a RawMode that reports IsTerminal() = true
	// Since RawMode.IsTerminal() checks term.IsTerminal(fd), we use the write end
	// of the pipe which won't be a terminal, but we can work around this by
	// testing the Restore method behavior instead.

	// For this test, we verify that when stdout IS writable, the sequence is written.
	// Create a handler with a custom fd that simulates terminal behavior.
	handler := &PTYHandler{
		stdin:   os.Stdin,
		stdout:  w,
		stderr:  os.Stderr,
		log:     logger.Nop(),
		rawMode: term.NewRawMode(int(w.Fd())),
	}

	// Since the pipe fd is not a real terminal, IsTerminal() will return false.
	// To properly test the write behavior, we need to mock the terminal check.
	// This test verifies that when IsTerminal returns false, nothing is written.
	handler.resetVisualStateUnlocked()

	// Close write end to allow read to complete
	w.Close()

	// Read any output
	buf := make([]byte, 256)
	n, _ := r.Read(buf)

	// Since IsTerminal() returns false for pipes, no output should be written
	if n != 0 {
		t.Errorf("expected no output for non-terminal, got %d bytes: %q", n, buf[:n])
	}
}

func TestResetVisualStateUnlocked_NonTerminal(t *testing.T) {
	// Create a pipe to capture stdout output
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	handler := &PTYHandler{
		stdin:   os.Stdin,
		stdout:  w,
		stderr:  os.Stderr,
		log:     logger.Nop(),
		rawMode: term.NewRawMode(int(w.Fd())), // pipe is not a terminal
	}

	// Call resetVisualStateUnlocked - should be no-op for non-terminal
	handler.resetVisualStateUnlocked()

	// Close write end to allow read to complete
	w.Close()

	// Read any output
	buf := make([]byte, 256)
	n, _ := r.Read(buf)

	// Verify no output was written
	if n != 0 {
		t.Errorf("expected no output for non-terminal, got %d bytes: %q", n, buf[:n])
	}
}

func TestRestore_ResetsVisualState(t *testing.T) {
	// Create a pipe to capture stdout output
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	handler := &PTYHandler{
		stdin:   os.Stdin,
		stdout:  w,
		stderr:  os.Stderr,
		log:     logger.Nop(),
		rawMode: term.NewRawMode(int(w.Fd())), // pipe is not a terminal
	}

	// Call Restore - should attempt to reset visual state then restore termios
	err = handler.Restore()
	if err != nil {
		t.Errorf("Restore() returned error: %v", err)
	}

	// Close write end to allow read to complete
	w.Close()

	// Read any output
	buf := make([]byte, 256)
	n, _ := r.Read(buf)

	// Since IsTerminal() returns false for pipes, no reset sequence should be written
	if n != 0 {
		t.Errorf("expected no reset output for non-terminal, got %d bytes: %q", n, buf[:n])
	}
}

func TestRestore_NoErrorOnNonTerminal(t *testing.T) {
	// Create a handler with non-terminal stdout
	handler := &PTYHandler{
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		log:     logger.Nop(),
		rawMode: term.NewRawMode(-1), // invalid fd, but IsTerminal will return false
	}

	// Restore should succeed (no-op) for non-terminal
	err := handler.Restore()
	if err != nil {
		t.Errorf("Restore() should not error for non-terminal, got: %v", err)
	}
}

// TestRestoreSequence pins the reset Restore writes. The mouse/paste/focus/cursor/
// SGR resets are idempotent and must appear in BOTH branches (a detached or killed
// session must never strand the host terminal in mouse-reporting mode — the
// "<35;95;29M" symptom). The alt-screen leave is the lone non-idempotent reset
// (DECRC squash) and must appear ONLY when the container left us in the alt buffer.
func TestRestoreSequence(t *testing.T) {
	alwaysOn := []string{
		"\x1b[?25h",   // show cursor
		"\x1b[?1000l", // mouse button tracking off
		"\x1b[?1002l", // mouse cell-motion off
		"\x1b[?1003l", // mouse all-motion off
		"\x1b[?1006l", // SGR mouse encoding off
		"\x1b[?1004l", // focus reporting off
		"\x1b[?2004l", // bracketed paste off
		"\x1b[0m",     // SGR reset
	}

	for _, inAlt := range []bool{false, true} {
		seq := restoreSequence(inAlt)
		for _, want := range alwaysOn {
			if !strings.Contains(seq, want) {
				t.Errorf("restoreSequence(%v) missing %q; got %q", inAlt, want, seq)
			}
		}
		if hasLeave := strings.Contains(seq, altScreenLeaveSeq); hasLeave != inAlt {
			t.Errorf("restoreSequence(%v) alt-leave present=%v, want %v", inAlt, hasLeave, inAlt)
		}
	}

	// When emitted, the alt-screen leave comes FIRST — leave the alt buffer before
	// the visual resets, never after.
	if withAlt := restoreSequence(true); !strings.HasPrefix(withAlt, altScreenLeaveSeq) {
		t.Errorf("alt-screen leave must be the prefix; got %q", withAlt)
	}
}

func TestIsClosedConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "closed connection error",
			err:      fmt.Errorf("read unix ->/var/run/docker.sock: use of closed network connection"),
			expected: true,
		},
		{
			name:     "wrapped closed connection error",
			err:      fmt.Errorf("copy failed: %w", fmt.Errorf("use of closed network connection")),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      fmt.Errorf("connection refused"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isClosedConnectionError(tt.err)
			if got != tt.expected {
				t.Errorf("isClosedConnectionError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestAltScreenTrackingWriter_ForwardsBytesUnchanged(t *testing.T) {
	var buf bytes.Buffer
	var flag atomic.Bool
	w := newAltScreenTrackingWriter(&buf, &flag)

	chunks := [][]byte{
		[]byte("hello "),
		[]byte("\x1b[?1049hworld"),
		[]byte("\x1b[?1049l done"),
	}
	var want []byte
	for _, c := range chunks {
		n, err := w.Write(c)
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != len(c) {
			t.Fatalf("Write returned n=%d, want %d", n, len(c))
		}
		want = append(want, c...)
	}
	if got := buf.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("forwarded bytes mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestAltScreenTrackingWriter_Flag(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   bool
	}{
		{"plain output never sets flag", []string{"just some text\n", "more text"}, false},
		{"enter only", []string{"\x1b[?1049h"}, true},
		{"enter then clean leave", []string{"\x1b[?1049h", "tui frame", "\x1b[?1049l"}, false},
		{"leave without enter", []string{"\x1b[?1049l"}, false},
		{"enter after leave wins", []string{"\x1b[?1049l", "\x1b[?1049h"}, true},
		{"both in one write, leave last", []string{"\x1b[?1049hX\x1b[?1049l"}, false},
		{"both in one write, enter last", []string{"\x1b[?1049l\x1b[?1049h"}, true},
		{"legacy 47h variant", []string{"\x1b[?47h"}, true},
		{"legacy 1047 enter then leave", []string{"\x1b[?1047h", "\x1b[?1047l"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flag atomic.Bool
			w := newAltScreenTrackingWriter(io.Discard, &flag)
			for _, s := range tt.writes {
				if _, err := w.Write([]byte(s)); err != nil {
					t.Fatalf("Write error: %v", err)
				}
			}
			if got := flag.Load(); got != tt.want {
				t.Errorf("flag = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAltScreenTrackingWriter_SplitSequence(t *testing.T) {
	// The alt-screen enter sequence is split across two Write calls at every
	// interior boundary; the carry must reassemble it so the flag still sets.
	const enter = "\x1b[?1049h"
	for i := 1; i < len(enter); i++ {
		t.Run(fmt.Sprintf("split_at_%d", i), func(t *testing.T) {
			var flag atomic.Bool
			w := newAltScreenTrackingWriter(io.Discard, &flag)
			if _, err := w.Write([]byte("prefix" + enter[:i])); err != nil {
				t.Fatalf("Write error: %v", err)
			}
			if _, err := w.Write([]byte(enter[i:] + "suffix")); err != nil {
				t.Fatalf("Write error: %v", err)
			}
			if !flag.Load() {
				t.Errorf("split at %d: flag not set; carry failed to reassemble %q", i, enter)
			}
		})
	}
}
