//go:build !windows

package iostreams

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/briandowns/spinner"
)

func TestProgressIndicator_TextMode_DefaultLabel(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.StartProgressIndicatorWithLabel("")

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Working...") {
		t.Errorf("expected 'Working...', got %q", output)
	}
}

func TestProgressIndicator_TextMode_WithLabel(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.StartProgressIndicatorWithLabel("Building")

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "Building...") {
		t.Errorf("expected 'Building...', got %q", output)
	}
}

func TestProgressIndicator_TextMode_LabelWithEllipsis(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.StartProgressIndicatorWithLabel("Loading...")

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
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.StartProgressIndicatorWithLabel("First")
	ios.StartProgressIndicatorWithLabel("Second")

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
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(false)

	ios.StartProgressIndicatorWithLabel("Test")
	ios.StopProgressIndicator()

	if ios.ErrBuf.String() != "" {
		t.Errorf("expected no output when disabled, got %q", ios.ErrBuf.String())
	}
}

func TestProgressIndicator_StopWithoutStart(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	// Should not panic
	ios.StopProgressIndicator()
}

func TestProgressIndicator_StopTwice(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	ios.StartProgressIndicatorWithLabel("Test")
	ios.StopProgressIndicator()
	ios.StopProgressIndicator() // Should not panic
}

func TestProgressIndicator_EnvVar(t *testing.T) {
	t.Setenv("CLAWKER_SPINNER_DISABLED", "1")

	ios := NewIOStreams()
	if !ios.spinnerDisabled {
		t.Error("spinnerDisabled should be true when env var set")
	}
}

func TestProgressIndicator_GetSetSpinnerDisabled(t *testing.T) {
	ios := NewTestIOStreams()

	if ios.GetSpinnerDisabled() {
		t.Error("default should be false")
	}

	ios.SetSpinnerDisabled(true)
	if !ios.GetSpinnerDisabled() {
		t.Error("should be true after SetSpinnerDisabled(true)")
	}
}

func TestProgressIndicator_RunWithProgress(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	called := false
	err := ios.RunWithProgress("Processing", func() error {
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
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	// spinnerDisabled defaults to false - use animated spinner

	ios.StartProgressIndicatorWithLabel("Loading")
	// Spinner library may not output to non-TTY writers,
	// but we can verify the spinner was created and can be stopped without panic
	time.Sleep(50 * time.Millisecond)
	ios.StopProgressIndicator()

	// Verify the spinner was properly managed (no panic, no deadlock)
	// The spinner library doesn't output to non-TTY buffers, so we can't verify
	// visual output in unit tests. The key is that Start/Stop work correctly.
}

func TestProgressIndicator_AnimatedSpinner_LabelUpdate(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	ios.StartProgressIndicatorWithLabel("First")
	time.Sleep(50 * time.Millisecond)
	ios.StartProgressIndicatorWithLabel("Second") // Should update prefix, not create new
	time.Sleep(50 * time.Millisecond)
	ios.StopProgressIndicator()

	// Verify the spinner was properly managed (no panic, no deadlock)
	// and that label update doesn't cause issues.
	// We can't verify visual output in non-TTY tests.
}

func TestProgressIndicator_AnimatedSpinner_InternalState(t *testing.T) {
	// Test that the spinner is properly tracked internally
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	// Start spinner - progressIndicator should be set
	ios.StartProgressIndicatorWithLabel("Test")

	// Access internal state via lock for verification
	ios.progressIndicatorMu.Lock()
	hasSpinner := ios.progressIndicator != nil
	ios.progressIndicatorMu.Unlock()

	if !hasSpinner {
		t.Error("spinner should be created after StartProgressIndicatorWithLabel")
	}

	// Stop spinner - progressIndicator should be nil
	ios.StopProgressIndicator()

	ios.progressIndicatorMu.Lock()
	hasSpinnerAfterStop := ios.progressIndicator != nil
	ios.progressIndicatorMu.Unlock()

	if hasSpinnerAfterStop {
		t.Error("spinner should be nil after StopProgressIndicator")
	}
}

func TestProgressIndicator_ConcurrentAccess(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ios.StartProgressIndicatorWithLabel("concurrent")
		}()
		go func() {
			defer wg.Done()
			ios.StopProgressIndicator()
		}()
	}
	wg.Wait()
	// Test passes if no panic/deadlock/race
}

func TestProgressIndicator_RunWithProgress_Error(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetSpinnerDisabled(true)

	expectedErr := errors.New("task failed")
	err := ios.RunWithProgress("Processing", func() error {
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

func TestProgressIndicator_ColorIsValid(t *testing.T) {
	// Verify "fgCyan" is a valid spinner color
	sp := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
	err := sp.Color("fgCyan")
	if err != nil {
		t.Fatalf("fgCyan should be a valid color: %v", err)
	}
}
