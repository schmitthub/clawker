// Package cmdutil provides utilities for command-line applications.
package cmdutil

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams provides access to standard input/output/error streams.
// It follows the GitHub CLI pattern for testable I/O.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// isInputTTY caches whether stdin is a terminal.
	// -1 = unchecked, 0 = false, 1 = true
	isInputTTY int

	// isOutputTTY caches whether stdout is a terminal.
	isOutputTTY int
}

// NewIOStreams creates an IOStreams connected to standard streams.
func NewIOStreams() *IOStreams {
	return &IOStreams{
		In:          os.Stdin,
		Out:         os.Stdout,
		ErrOut:      os.Stderr,
		isInputTTY:  -1,
		isOutputTTY: -1,
	}
}

// IsInputTTY returns true if stdin is a terminal.
func (s *IOStreams) IsInputTTY() bool {
	if s.isInputTTY == -1 {
		if f, ok := s.In.(*os.File); ok {
			s.isInputTTY = boolToInt(term.IsTerminal(int(f.Fd())))
		} else {
			s.isInputTTY = 0
		}
	}
	return s.isInputTTY == 1
}

// IsOutputTTY returns true if stdout is a terminal.
func (s *IOStreams) IsOutputTTY() bool {
	if s.isOutputTTY == -1 {
		if f, ok := s.Out.(*os.File); ok {
			s.isOutputTTY = boolToInt(term.IsTerminal(int(f.Fd())))
		} else {
			s.isOutputTTY = 0
		}
	}
	return s.isOutputTTY == 1
}

// IsInteractive returns true if both stdin and stdout are terminals.
// When false, commands should behave as if --yes was passed (for CI).
func (s *IOStreams) IsInteractive() bool {
	return s.IsInputTTY() && s.IsOutputTTY()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestIOStreams creates IOStreams for testing with bytes.Buffer.
// Returns the streams and separate buffers for stdout and stderr.
type TestIOStreams struct {
	*IOStreams
	InBuf  *testBuffer
	OutBuf *testBuffer
	ErrBuf *testBuffer
}

// testBuffer wraps a byte slice for use in tests.
type testBuffer struct {
	data []byte
}

func (b *testBuffer) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *testBuffer) String() string {
	return string(b.data)
}

func (b *testBuffer) Reset() {
	b.data = nil
}

// SetInput sets the input data for the test buffer.
func (b *testBuffer) SetInput(s string) {
	b.data = []byte(s)
}

// NewTestIOStreams creates IOStreams for testing.
func NewTestIOStreams() *TestIOStreams {
	in := &testBuffer{}
	out := &testBuffer{}
	errOut := &testBuffer{}

	return &TestIOStreams{
		IOStreams: &IOStreams{
			In:          in,
			Out:         out,
			ErrOut:      errOut,
			isInputTTY:  0, // Tests are non-interactive by default
			isOutputTTY: 0,
		},
		InBuf:  in,
		OutBuf: out,
		ErrBuf: errOut,
	}
}

// SetInteractive allows tests to simulate interactive mode.
func (t *TestIOStreams) SetInteractive(interactive bool) {
	if interactive {
		t.IOStreams.isInputTTY = 1
		t.IOStreams.isOutputTTY = 1
	} else {
		t.IOStreams.isInputTTY = 0
		t.IOStreams.isOutputTTY = 0
	}
}
