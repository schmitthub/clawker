// Package iostreamstest provides test doubles for the iostreams package.
// All test files should use iostreamstest.New() to get properly wired
// IOStreams with a loggertest nop logger.
package iostreamstest

import (
	"io"
	"sync"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger/loggertest"
)

// New creates IOStreams for testing.
// Non-interactive, colors disabled, nop logger by default.
func New() *TestIOStreams {
	in := &testBuffer{}
	out := &testBuffer{}
	errOut := &testBuffer{}

	ios := &iostreams.IOStreams{
		In:     in,
		Out:    out,
		ErrOut: errOut,
		Logger: loggertest.NewNop(),
	}

	// Struct literal zero-values give us non-interactive (isInputTTY=0,
	// isOutputTTY=0, isStderrTTY=0) and auto-detect color disabled
	// (colorEnabled=0 means disabled; -1 would mean auto-detect).

	return &TestIOStreams{
		IOStreams: ios,
		InBuf:    in,
		OutBuf:   out,
		ErrBuf:   errOut,
	}
}

// TestIOStreams wraps IOStreams for testing with accessible buffers.
type TestIOStreams struct {
	*iostreams.IOStreams
	InBuf  *testBuffer
	OutBuf *testBuffer
	ErrBuf *testBuffer
}

// testBuffer wraps a byte slice for use in tests.
type testBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *testBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *testBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func (b *testBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = nil
}

// SetInput sets the input data for the test buffer.
func (b *testBuffer) SetInput(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = []byte(s)
}

// SetInteractive allows tests to simulate interactive mode.
func (t *TestIOStreams) SetInteractive(interactive bool) {
	t.IOStreams.SetStdinTTY(interactive)
	t.IOStreams.SetStdoutTTY(interactive)
	t.IOStreams.SetStderrTTY(interactive)
}

// SetColorEnabled allows tests to control color output.
func (t *TestIOStreams) SetColorEnabled(enabled bool) {
	t.IOStreams.SetColorEnabled(enabled)
}

// SetTerminalSize allows tests to simulate terminal size.
func (t *TestIOStreams) SetTerminalSize(width, height int) {
	t.IOStreams.SetTerminalSizeCache(width, height)
}

// SetProgressEnabled allows tests to enable/disable progress indicator.
func (t *TestIOStreams) SetProgressEnabled(enabled bool) {
	t.IOStreams.SetProgressIndicatorEnabled(enabled)
}

// SetSpinnerDisabled allows tests to disable the animated spinner.
func (t *TestIOStreams) SetSpinnerDisabled(disabled bool) {
	t.IOStreams.SetSpinnerDisabled(disabled)
}
