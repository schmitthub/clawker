package iostreams

import (
	"io"
	"sync"
	"testing"

	"github.com/schmitthub/clawker/internal/logger/loggertest"
	"github.com/stretchr/testify/assert"
)

type iostreamstestShim struct{}

var iostreamstest iostreamstestShim

type testIOStreams struct {
	*IOStreams
	InBuf  *testBuffer
	OutBuf *testBuffer
	ErrBuf *testBuffer
}

type testBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (iostreamstestShim) New() *testIOStreams {
	in := &testBuffer{}
	out := &testBuffer{}
	errOut := &testBuffer{}

	ios := &IOStreams{
		In:     in,
		Out:    out,
		ErrOut: errOut,
		Logger: loggertest.NewNop(),
	}

	return &testIOStreams{
		IOStreams: ios,
		InBuf:     in,
		OutBuf:    out,
		ErrBuf:    errOut,
	}
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

func (b *testBuffer) SetInput(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = []byte(s)
}

func (t *testIOStreams) SetInteractive(interactive bool) {
	t.IOStreams.SetStdinTTY(interactive)
	t.IOStreams.SetStdoutTTY(interactive)
	t.IOStreams.SetStderrTTY(interactive)
}

func (t *testIOStreams) SetColorEnabled(enabled bool) {
	t.IOStreams.SetColorEnabled(enabled)
}

func (t *testIOStreams) SetTerminalSize(width, height int) {
	t.IOStreams.SetTerminalSizeCache(width, height)
}

func (t *testIOStreams) SetProgressEnabled(enabled bool) {
	t.IOStreams.SetProgressIndicatorEnabled(enabled)
}

func (t *testIOStreams) SetSpinnerDisabled(disabled bool) {
	t.IOStreams.SetSpinnerDisabled(disabled)
}

func TestSystem_BasicSetup(t *testing.T) {
	ios := System()

	if ios == nil {
		t.Fatal("System() returned nil")
	}
	if ios.In == nil {
		t.Error("System().In is nil")
	}
	if ios.Out == nil {
		t.Error("System().Out is nil")
	}
	if ios.ErrOut == nil {
		t.Error("System().ErrOut is nil")
	}
	// isInputTTY should still be lazy-loaded (not queried during construction)
	if ios.isInputTTY != -1 {
		t.Errorf("System().isInputTTY = %d, want -1", ios.isInputTTY)
	}
	// isOutputTTY is now queried during construction to check for progress indicator enablement.
	// It will be 0 (not a TTY) when running in tests.
	if ios.isOutputTTY == -1 {
		t.Error("System().isOutputTTY should be detected during construction")
	}
	// Note: isStderrTTY may still be -1 due to short-circuit evaluation in the progress check.
	// If stdout is not a TTY, stderr check is skipped. This is fine - it will be lazy-loaded on demand.
}

func TestIOStreams_TTY(t *testing.T) {
	// Test with non-TTY (test buffers)
	ios := iostreamstest.New()

	// Default is non-interactive
	if ios.IsInputTTY() {
		t.Error("TestIOStreams should not be input TTY by default")
	}
	if ios.IsOutputTTY() {
		t.Error("TestIOStreams should not be output TTY by default")
	}
	if ios.IsInteractive() {
		t.Error("TestIOStreams should not be interactive by default")
	}
}

func TestNewIOStreams_New(t *testing.T) {
	ios := iostreamstest.New()

	if ios == nil {
		t.Fatal("iostreamstest.New() returned nil")
	}
	if ios.IOStreams == nil {
		t.Fatal("iostreamstest.New().IOStreams is nil")
	}
	if ios.InBuf == nil {
		t.Error("iostreamstest.New().InBuf is nil")
	}
	if ios.OutBuf == nil {
		t.Error("iostreamstest.New().OutBuf is nil")
	}
	if ios.ErrBuf == nil {
		t.Error("iostreamstest.New().ErrBuf is nil")
	}

	// Verify buffers are connected to IOStreams
	if ios.IOStreams.In != ios.InBuf {
		t.Error("IOStreams.In is not connected to InBuf")
	}
	if ios.IOStreams.Out != ios.OutBuf {
		t.Error("IOStreams.Out is not connected to OutBuf")
	}
	if ios.IOStreams.ErrOut != ios.ErrBuf {
		t.Error("IOStreams.ErrOut is not connected to ErrBuf")
	}

	// Verify non-interactive by default
	if ios.isInputTTY != 0 {
		t.Errorf("iostreamstest.New().isInputTTY = %d, want 0", ios.isInputTTY)
	}
	if ios.isOutputTTY != 0 {
		t.Errorf("iostreamstest.New().isOutputTTY = %d, want 0", ios.isOutputTTY)
	}
}

func TestSetInteractive(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		wantInput   bool
		wantOutput  bool
	}{
		{
			name:        "set interactive true",
			interactive: true,
			wantInput:   true,
			wantOutput:  true,
		},
		{
			name:        "set interactive false",
			interactive: false,
			wantInput:   false,
			wantOutput:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			ios.SetInteractive(tt.interactive)

			if ios.IsInputTTY() != tt.wantInput {
				t.Errorf("IsInputTTY() = %v, want %v", ios.IsInputTTY(), tt.wantInput)
			}
			if ios.IsOutputTTY() != tt.wantOutput {
				t.Errorf("IsOutputTTY() = %v, want %v", ios.IsOutputTTY(), tt.wantOutput)
			}
			if ios.IsInteractive() != (tt.wantInput && tt.wantOutput) {
				t.Errorf("IsInteractive() = %v, want %v", ios.IsInteractive(), tt.wantInput && tt.wantOutput)
			}
		})
	}
}

func TestTestBuffer(t *testing.T) {
	t.Run("Write and String", func(t *testing.T) {
		ios := iostreamstest.New()
		buf := ios.OutBuf
		n, err := buf.Write([]byte("hello"))
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Write() = %d, want 5", n)
		}
		if buf.String() != "hello" {
			t.Errorf("String() = %q, want %q", buf.String(), "hello")
		}
	})

	t.Run("Read", func(t *testing.T) {
		ios := iostreamstest.New()
		buf := ios.InBuf
		buf.SetInput("world")

		p := make([]byte, 10)
		n, err := buf.Read(p)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Read() = %d, want 5", n)
		}
		if string(p[:n]) != "world" {
			t.Errorf("Read() data = %q, want %q", string(p[:n]), "world")
		}
	})

	t.Run("Read empty returns EOF", func(t *testing.T) {
		ios := iostreamstest.New()
		buf := ios.InBuf
		p := make([]byte, 10)
		n, err := buf.Read(p)
		if n != 0 {
			t.Errorf("Read() = %d, want 0", n)
		}
		if err != io.EOF {
			t.Errorf("Read() error = %v, want EOF", err)
		}
	})

	t.Run("Reset", func(t *testing.T) {
		ios := iostreamstest.New()
		buf := ios.OutBuf
		buf.Write([]byte("hello"))
		buf.Reset()
		if buf.String() != "" {
			t.Errorf("String() after Reset() = %q, want empty", buf.String())
		}
	})

	t.Run("SetInput", func(t *testing.T) {
		ios := iostreamstest.New()
		buf := ios.InBuf
		buf.SetInput("test input")
		if buf.String() != "test input" {
			t.Errorf("String() after SetInput() = %q, want %q", buf.String(), "test input")
		}
	})
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should return 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should return 0")
	}
}

func TestIOStreams_IsStderrTTY(t *testing.T) {
	ios := iostreamstest.New()

	// Default is non-TTY for test streams
	if ios.IsStderrTTY() {
		t.Error("TestIOStreams should not be stderr TTY by default")
	}
}

func TestIOStreams_ColorEnabled(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*IOStreams)
		wantEnabled bool
	}{
		{
			name:        "auto-detect non-TTY",
			setup:       func(s *IOStreams) {},
			wantEnabled: false, // Non-TTY = no color
		},
		{
			name:        "explicitly enabled",
			setup:       func(s *IOStreams) { s.SetColorEnabled(true) },
			wantEnabled: true,
		},
		{
			name:        "explicitly disabled",
			setup:       func(s *IOStreams) { s.SetColorEnabled(false) },
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			tt.setup(ios.IOStreams)

			if ios.ColorEnabled() != tt.wantEnabled {
				t.Errorf("ColorEnabled() = %v, want %v", ios.ColorEnabled(), tt.wantEnabled)
			}
		})
	}
}

func TestIOStreams_TerminalTheme(t *testing.T) {
	ios := iostreamstest.New()

	// Test DetectTerminalTheme
	ios.IOStreams.DetectTerminalTheme()
	theme := ios.TerminalTheme()

	// For non-TTY, should be "none"
	if theme != "none" {
		t.Errorf("TerminalTheme() = %q, want %q for non-TTY", theme, "none")
	}
}

func TestIOStreams_ColorScheme(t *testing.T) {
	ios := iostreamstest.New()
	cs := ios.ColorScheme()

	if cs == nil {
		t.Fatal("ColorScheme() returned nil")
	}

	// Colors should be disabled for test streams
	if cs.Enabled() {
		t.Error("ColorScheme.Enabled() should be false for test streams")
	}
}

func TestIOStreams_TerminalSize(t *testing.T) {
	ios := iostreamstest.New()

	// Set a custom size
	ios.SetTerminalSize(120, 40)

	w, h := ios.TerminalSize()
	if w != 120 {
		t.Errorf("TerminalWidth() = %d, want 120", w)
	}
	if h != 40 {
		t.Errorf("TerminalHeight() = %d, want 40", h)
	}

	// Test TerminalWidth() shortcut
	if ios.TerminalWidth() != 120 {
		t.Errorf("TerminalWidth() = %d, want 120", ios.TerminalWidth())
	}

	// Test cache invalidation
	ios.InvalidateTerminalSizeCache()
	// After invalidation, should return defaults for test buffer (80x24)
	w, h = ios.TerminalSize()
	if w != 80 {
		t.Errorf("After invalidation, TerminalWidth() = %d, want 80", w)
	}
	if h != 24 {
		t.Errorf("After invalidation, TerminalHeight() = %d, want 24", h)
	}
}

func TestIOStreams_CanPrompt(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		neverPrompt bool
		want        bool
	}{
		{
			name:        "interactive, prompts allowed",
			interactive: true,
			neverPrompt: false,
			want:        true,
		},
		{
			name:        "interactive, prompts disabled",
			interactive: true,
			neverPrompt: true,
			want:        false,
		},
		{
			name:        "non-interactive",
			interactive: false,
			neverPrompt: false,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			ios.SetInteractive(tt.interactive)
			ios.IOStreams.SetNeverPrompt(tt.neverPrompt)

			if ios.CanPrompt() != tt.want {
				t.Errorf("CanPrompt() = %v, want %v", ios.CanPrompt(), tt.want)
			}
		})
	}
}

func TestIOStreams_NeverPrompt(t *testing.T) {
	ios := iostreamstest.New()

	// Default is false
	if ios.GetNeverPrompt() {
		t.Error("GetNeverPrompt() should be false by default")
	}

	// Set to true
	ios.IOStreams.SetNeverPrompt(true)
	if !ios.GetNeverPrompt() {
		t.Error("GetNeverPrompt() should be true after SetNeverPrompt(true)")
	}

	// Set back to false
	ios.IOStreams.SetNeverPrompt(false)
	if ios.GetNeverPrompt() {
		t.Error("GetNeverPrompt() should be false after SetNeverPrompt(false)")
	}
}

func TestIOStreams_Progress(t *testing.T) {
	ios := iostreamstest.New()

	// Start and stop progress (non-TTY should be a no-op but not panic)
	ios.StartProgressIndicator()
	ios.StopProgressIndicator()

	// Start with label
	ios.StartProgressIndicatorWithLabel("Loading...")
	ios.StopProgressIndicator()

	// RunWithProgress
	var called bool
	err := ios.RunWithProgress("Processing", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Errorf("RunWithProgress() error = %v", err)
	}
	if !called {
		t.Error("RunWithProgress() should have called the function")
	}
}

func TestIOStreams_Pager(t *testing.T) {
	ios := iostreamstest.New()

	// Set custom pager
	ios.IOStreams.SetPager("cat")
	if ios.IOStreams.GetPager() != "cat" {
		t.Errorf("GetPager() = %q, want %q", ios.IOStreams.GetPager(), "cat")
	}

	// Clear pager
	ios.IOStreams.SetPager("")
	// Should return platform default
	pager := ios.IOStreams.GetPager()
	if pager == "" {
		t.Error("GetPager() should return platform default when not explicitly set")
	}
}

func TestIOStreams_AlternateScreen(t *testing.T) {
	ios := iostreamstest.New()

	// Enable and start (non-TTY should be a no-op)
	ios.IOStreams.SetAlternateScreenBufferEnabled(true)
	ios.IOStreams.StartAlternateScreenBuffer()
	ios.IOStreams.StopAlternateScreenBuffer()

	// RefreshScreen (non-TTY should be a no-op)
	ios.IOStreams.RefreshScreen()
}

func TestTestIOStreams_SetColorEnabled(t *testing.T) {
	ios := iostreamstest.New()

	// Default colors disabled
	if ios.ColorEnabled() {
		t.Error("ColorEnabled() should be false by default for test streams")
	}

	// Enable colors
	ios.SetColorEnabled(true)
	if !ios.ColorEnabled() {
		t.Error("ColorEnabled() should be true after SetColorEnabled(true)")
	}

	// Disable colors
	ios.SetColorEnabled(false)
	if ios.ColorEnabled() {
		t.Error("ColorEnabled() should be false after SetColorEnabled(false)")
	}
}

func TestSystem_TermCapabilities(t *testing.T) {
	ios := System()
	// Should not panic
	_ = ios.Is256ColorSupported()
	_ = ios.IsTrueColorSupported()
}

func TestIostreamstestNew_TermNil(t *testing.T) {
	tio := iostreamstest.New()
	assert.False(t, tio.Is256ColorSupported())
	assert.False(t, tio.IsTrueColorSupported())
}
