package iostreams

import (
	"strings"
	"sync"
	"testing"
)

func TestNewProgressBar(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(100, "Building")
	if pb == nil {
		t.Fatal("NewProgressBar should return non-nil")
	}
}

func TestNewProgressBar_Disabled(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(false)

	pb := ios.IOStreams.NewProgressBar(100, "Building")
	if pb == nil {
		t.Fatal("NewProgressBar should return non-nil even when disabled")
	}

	// All operations should be no-ops when disabled
	pb.Set(50)
	pb.Increment()
	pb.Finish()

	if ios.ErrBuf.String() != "" {
		t.Errorf("expected no output when disabled, got %q", ios.ErrBuf.String())
	}
}

func TestProgressBar_Set(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(100, "Building")
	pb.Set(50)

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "50%") {
		t.Errorf("expected '50%%' in output, got %q", output)
	}
	if !strings.Contains(output, "Building") {
		t.Errorf("expected 'Building' label in output, got %q", output)
	}
}

func TestProgressBar_Increment(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(4, "Steps")
	pb.Increment()
	pb.Increment()

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "50%") {
		t.Errorf("expected '50%%' after 2/4 increments, got %q", output)
	}
}

func TestProgressBar_Finish(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(10, "Loading")
	pb.Set(5)
	ios.ErrBuf.Reset()
	pb.Finish()

	output := ios.ErrBuf.String()
	if !strings.Contains(output, "100%") {
		t.Errorf("expected '100%%' after Finish, got %q", output)
	}
}

func TestProgressBar_NonTTY_PeriodicUpdates(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	// Non-TTY by default in test — should print periodic line updates

	pb := ios.IOStreams.NewProgressBar(100, "Processing")

	// Set to various percentages — non-TTY should print at thresholds
	for i := 1; i <= 100; i++ {
		pb.Set(i)
	}
	pb.Finish()

	output := ios.ErrBuf.String()
	// Non-TTY should have newline-separated updates
	if !strings.Contains(output, "Processing") {
		t.Errorf("expected 'Processing' in non-TTY output, got %q", output)
	}
	// Should have the final 100%
	if !strings.Contains(output, "100%") {
		t.Errorf("expected '100%%' in non-TTY output, got %q", output)
	}
}

func TestProgressBar_TTY_AnimatedBar(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetInteractive(true)
	ios.SetColorEnabled(true)

	pb := ios.IOStreams.NewProgressBar(20, "Building")
	pb.Set(10)

	output := ios.ErrBuf.String()
	// TTY mode uses \r for overwrite
	if !strings.Contains(output, "\r") {
		t.Errorf("TTY mode should use \\r, got %q", output)
	}
	if !strings.Contains(output, "50%") {
		t.Errorf("expected '50%%' in TTY output, got %q", output)
	}
}

func TestProgressBar_ClampValues(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(10, "Test")

	// Negative value should clamp to 0
	pb.Set(-5)
	output := ios.ErrBuf.String()
	if !strings.Contains(output, "0%") {
		t.Errorf("negative value should clamp to 0%%, got %q", output)
	}

	ios.ErrBuf.Reset()

	// Over-max value should clamp to 100
	pb.Set(999)
	output = ios.ErrBuf.String()
	if !strings.Contains(output, "100%") {
		t.Errorf("over-max value should clamp to 100%%, got %q", output)
	}
}

func TestProgressBar_ZeroTotal(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(0, "Test")

	// Set on zero-total should not panic
	pb.Set(1)
	pb.Increment()
	pb.Finish()
	// Should not panic — that's the test
}

func TestProgressBar_ThreadSafety(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(100, "Concurrent")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			pb.Set(val)
		}(i * 5)
	}
	wg.Wait()
	pb.Finish()
	// Test passes if no panic/deadlock/race
}

func TestProgressBar_FinishTwice(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)

	pb := ios.IOStreams.NewProgressBar(10, "Test")
	pb.Finish()
	pb.Finish() // Should not panic
}

func TestProgressBar_BarVisuals(t *testing.T) {
	ios := NewTestIOStreams()
	ios.SetProgressEnabled(true)
	ios.SetInteractive(true)

	pb := ios.IOStreams.NewProgressBar(20, "Building")
	pb.Set(9)

	output := ios.ErrBuf.String()
	// Should contain the fraction display
	if !strings.Contains(output, "9/20") {
		t.Errorf("expected '9/20' fraction in TTY output, got %q", output)
	}
}
