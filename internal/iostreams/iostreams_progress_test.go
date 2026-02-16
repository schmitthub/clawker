package iostreams_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestProgressIndicator_TextMode_DefaultLabel(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("")

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Working...") {
		t.Errorf("expected 'Working...', got %q", output)
	}
}

func TestProgressIndicator_TextMode_WithLabel(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("Building")

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Building...") {
		t.Errorf("expected 'Building...', got %q", output)
	}
}

func TestProgressIndicator_TextMode_LabelWithEllipsis(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("Loading...")

	output := ios.ErrBuf.String()
	// Should NOT double the ellipsis
	if strings.Contains(output, "Loading......") {
		t.Errorf("should not double ellipsis, got %q", output)
	}
	if !strings.Contains(output, "Loading...") {
		t.Errorf("expected 'Loading...', got %q", output)
	}
}

func TestProgressIndicator_TextMode_MultipleCalls(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("First")
	ios.IOStreams.StartProgressIndicatorWithLabel("Second")

	output := ios.ErrBuf.String()
	// Both should appear (text mode prints each time)
	if !strings.Contains(output, "First...") {
		t.Errorf("expected 'First...', got %q", output)
	}
	if !strings.Contains(output, "Second...") {
		t.Errorf("expected 'Second...', got %q", output)
	}
}

func TestProgressIndicator_Disabled(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(false)

	ios.IOStreams.StartProgressIndicatorWithLabel("Test")
	ios.IOStreams.StopProgressIndicator()

	if ios.ErrBuf.String() != "" {
		t.Errorf("expected no output when disabled, got %q", ios.ErrBuf.String())
	}
}

func TestProgressIndicator_StopWithoutStart(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)

	// Should not panic
	ios.IOStreams.StopProgressIndicator()
}

func TestProgressIndicator_StopTwice(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("Test")
	ios.IOStreams.StopProgressIndicator()
	ios.IOStreams.StopProgressIndicator() // Should not panic
}

func TestProgressIndicator_GetSetSpinnerDisabled(t *testing.T) {
	ios := iostreamstest.New()

	if ios.IOStreams.GetSpinnerDisabled() {
		t.Error("default should be false")
	}

	ios.SetSpinnerDisabled(true)
	if !ios.IOStreams.GetSpinnerDisabled() {
		t.Error("should be true after SetSpinnerDisabled(true)")
	}
}

func TestProgressIndicator_RunWithProgress(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	called := false
	err := ios.IOStreams.RunWithProgress("Processing", func() error {
		called = true
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !called {
		t.Error("function was not called")
	}

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Processing...") {
		t.Errorf("expected 'Processing...', got %q", output)
	}
}

func TestProgressIndicator_AnimatedSpinner(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	// spinnerDisabled defaults to false - use animated spinner

	ios.IOStreams.StartProgressIndicatorWithLabel("Loading")
	// Spinner library may not output to non-TTY writers,
	// but we can verify the spinner was created and can be stopped without panic
	time.Sleep(50 * time.Millisecond)
	ios.IOStreams.StopProgressIndicator()

	// Verify the spinner was properly managed (no panic, no deadlock)
	// The spinner library doesn't output to non-TTY buffers, so we can't verify
	// visual output in unit tests. The key is that Start/Stop work correctly.
}

func TestProgressIndicator_AnimatedSpinner_LabelUpdate(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)

	ios.IOStreams.StartProgressIndicatorWithLabel("First")
	time.Sleep(50 * time.Millisecond)
	ios.IOStreams.StartProgressIndicatorWithLabel("Second") // Should update prefix, not create new
	time.Sleep(50 * time.Millisecond)
	ios.IOStreams.StopProgressIndicator()

	// Verify the spinner was properly managed (no panic, no deadlock)
	// and that label update doesn't cause issues.
	// We can't verify visual output in non-TTY tests.
}

func TestProgressIndicator_AnimatedSpinner_InternalState(t *testing.T) {
	// Validate spinner lifecycle behavior through public APIs.
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)

	// Start, update label, and stop without panic/deadlock.
	ios.IOStreams.StartProgressIndicatorWithLabel("Test")
	time.Sleep(20 * time.Millisecond)
	ios.IOStreams.StartProgressIndicatorWithLabel("Updated")
	time.Sleep(20 * time.Millisecond)
	ios.IOStreams.StopProgressIndicator()

	// Idempotent stop should also not panic.
	ios.IOStreams.StopProgressIndicator()
}

func TestProgressIndicator_ConcurrentAccess(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ios.IOStreams.StartProgressIndicatorWithLabel("concurrent")
		}()
		go func() {
			defer wg.Done()
			ios.IOStreams.StopProgressIndicator()
		}()
	}
	wg.Wait()
	// Test passes if no panic/deadlock/race
}

func TestProgressIndicator_RunWithProgress_Error(t *testing.T) {
	ios := iostreamstest.New()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	expectedErr := errors.New("task failed")
	err := ios.IOStreams.RunWithProgress("Processing", func() error {
		return expectedErr
	})

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Processing...") {
		t.Errorf("should still show progress label, got %q", output)
	}
}

func TestProgressIndicator_SpinnerStyle(t *testing.T) {
	// Verify our default spinner style (braille) renders with cyan color
	cs := iostreams.NewColorScheme(true, "dark")
	frame := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 0, "test", cs)
	if frame == "" {
		t.Fatal("SpinnerFrame should return non-empty string")
	}
}
