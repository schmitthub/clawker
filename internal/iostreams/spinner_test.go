package iostreams_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// --- SpinnerFrame pure function tests ---

func TestSpinnerFrame_BrailleType(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")
	frame := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 0, "Loading", cs)

	if frame == "" {
		t.Error("SpinnerFrame should return non-empty string")
	}
	if !strings.Contains(frame, "Loading") {
		t.Errorf("SpinnerFrame should contain label, got %q", frame)
	}
}

func TestSpinnerFrame_FramesCycle(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")

	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		frame := iostreams.SpinnerFrame(iostreams.SpinnerBraille, i, "", cs)
		seen[frame] = true
	}

	// Braille spinner has 10 frames, so we should see multiple distinct frames
	if len(seen) < 2 {
		t.Errorf("expected multiple distinct frames, got %d", len(seen))
	}
}

func TestSpinnerFrame_AllTypes(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")

	types := []iostreams.SpinnerType{
		iostreams.SpinnerBraille,
		iostreams.SpinnerDots,
		iostreams.SpinnerLine,
		iostreams.SpinnerPulse,
		iostreams.SpinnerGlobe,
		iostreams.SpinnerMoon,
	}

	for _, st := range types {
		frame := iostreams.SpinnerFrame(st, 0, "test", cs)
		if frame == "" {
			t.Errorf("iostreams.SpinnerFrame(%d, 0, ...) returned empty string", st)
		}
		if !strings.Contains(frame, "test") {
			t.Errorf("iostreams.SpinnerFrame(%d, ...) missing label, got %q", st, frame)
		}
	}
}

func TestSpinnerFrame_EmptyLabel(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")
	frame := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 0, "", cs)

	if frame == "" {
		t.Error("SpinnerFrame with empty label should still return spinner character")
	}
}

func TestSpinnerFrame_WithColors(t *testing.T) {
	csEnabled := iostreams.NewColorScheme(true, "dark")
	csDisabled := iostreams.NewColorScheme(false, "none")

	frameColored := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 0, "Loading", csEnabled)
	framePlain := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 0, "Loading", csDisabled)

	// Both should contain the label
	if !strings.Contains(frameColored, "Loading") {
		t.Errorf("colored frame should contain label, got %q", frameColored)
	}
	if !strings.Contains(framePlain, "Loading") {
		t.Errorf("plain frame should contain label, got %q", framePlain)
	}

	// Both should be non-empty and valid
	if frameColored == "" || framePlain == "" {
		t.Error("frames should be non-empty for both enabled and disabled color schemes")
	}
}

func TestSpinnerFrame_IsPure(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")

	// Same inputs should produce same output
	frame1 := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 3, "test", cs)
	frame2 := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 3, "test", cs)

	if frame1 != frame2 {
		t.Errorf("SpinnerFrame should be pure: %q != %q", frame1, frame2)
	}
}

func TestSpinnerFrame_WrapsAround(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")

	// Tick much larger than frame count should still work (modular arithmetic)
	frame := iostreams.SpinnerFrame(iostreams.SpinnerBraille, 1000, "test", cs)
	if frame == "" {
		t.Error("SpinnerFrame with large tick should wrap around, not fail")
	}
}

func TestSpinnerFrame_LineType(t *testing.T) {
	cs := iostreams.NewColorScheme(false, "none")

	// Line type uses ASCII: - \ | /
	frames := make([]string, 4)
	for i := 0; i < 4; i++ {
		frames[i] = iostreams.SpinnerFrame(iostreams.SpinnerLine, i, "", cs)
	}

	// Should have 4 distinct frames
	seen := make(map[string]bool)
	for _, f := range frames {
		seen[f] = true
	}
	if len(seen) != 4 {
		t.Errorf("Line spinner should have 4 distinct frames, got %d", len(seen))
	}
}

// --- spinnerFrames function tests ---

func TestSpinnerFrames(t *testing.T) {
	types := []iostreams.SpinnerType{
		iostreams.SpinnerBraille,
		iostreams.SpinnerDots,
		iostreams.SpinnerLine,
		iostreams.SpinnerPulse,
		iostreams.SpinnerGlobe,
		iostreams.SpinnerMoon,
	}

	for _, st := range types {
		frames := iostreams.SpinnerFrames(st)
		if len(frames) == 0 {
			t.Errorf("spinnerFrames(%d) returned empty slice", st)
		}
		for i, f := range frames {
			if f == "" {
				t.Errorf("spinnerFrames(%d)[%d] is empty", st, i)
			}
		}
	}
}

func TestSpinnerFrames_UnknownType(t *testing.T) {
	// Unknown type should fall back to braille
	frames := iostreams.SpinnerFrames(iostreams.SpinnerType(99))
	if len(frames) == 0 {
		t.Error("unknown spinner type should fall back to braille frames")
	}
}

// --- IOStreams spinner integration tests ---

func TestStartSpinner_TextFallback(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true) // text fallback mode

	ios.StartSpinner("Building")

	output := errOut.String()
	if !strings.Contains(output, "Building...") {
		t.Errorf("expected 'Building...' in text fallback, got %q", output)
	}
}

func TestStartSpinner_TextFallback_DefaultLabel(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	ios.StartSpinner("")

	output := errOut.String()
	if !strings.Contains(output, "Working...") {
		t.Errorf("expected default 'Working...' label, got %q", output)
	}
}

func TestStartSpinner_TextFallback_NoDoubleEllipsis(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	ios.StartSpinner("Loading...")

	output := errOut.String()
	if strings.Contains(output, "Loading......") {
		t.Errorf("should not double ellipsis, got %q", output)
	}
	if !strings.Contains(output, "Loading...") {
		t.Errorf("expected 'Loading...', got %q", output)
	}
}

func TestStartSpinner_Disabled(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.StartSpinner("Test")
	ios.StopSpinner()

	if errOut.String() != "" {
		t.Errorf("expected no output when disabled, got %q", errOut.String())
	}
}

func TestStopSpinner_WithoutStart(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	// Should not panic
	ios.StopSpinner()
}

func TestStopSpinner_Twice(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	ios.StartSpinner("Test")
	ios.StopSpinner()
	ios.StopSpinner() // Should not panic
}

func TestRunWithSpinner_CallsFunction(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	called := false
	err := ios.RunWithSpinner("Processing", func() error {
		called = true
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !called {
		t.Error("function was not called")
	}

	output := errOut.String()
	if !strings.Contains(output, "Processing...") {
		t.Errorf("expected 'Processing...' in output, got %q", output)
	}
}

func TestRunWithSpinner_PropagatesError(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	expectedErr := errors.New("task failed")
	err := ios.RunWithSpinner("Processing", func() error {
		return expectedErr
	})

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestStartSpinnerWithType(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	ios.StartSpinnerWithType(iostreams.SpinnerLine, "Building")

	output := errOut.String()
	if !strings.Contains(output, "Building...") {
		t.Errorf("expected 'Building...' in output, got %q", output)
	}
}

func TestSpinner_ConcurrentAccess(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ios.StartSpinner("concurrent")
		}()
		go func() {
			defer wg.Done()
			ios.StopSpinner()
		}()
	}
	wg.Wait()
	// Test passes if no panic/deadlock/race
}

func TestSpinner_ConcurrentAccess_AnimatedMode(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	// spinnerDisabled is false → animated mode

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			ios.StartSpinner("concurrent")
		}()
		go func() {
			defer wg.Done()
			ios.StopSpinner()
		}()
		go func(label string) {
			defer wg.Done()
			ios.StartSpinner(label)
		}(fmt.Sprintf("label-%d", i))
	}
	wg.Wait()
	ios.StopSpinner() // ensure cleanup
	// Test passes if no panic/deadlock/race
}

func TestStopSpinner_AnimatedMode_Twice(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	// spinnerDisabled is false — animated mode

	ios.StartSpinner("Test")
	// Small sleep to let goroutine start
	time.Sleep(10 * time.Millisecond)
	ios.StopSpinner()
	ios.StopSpinner() // Should not panic (sync.Once protects spinnerRunner.Stop)
}

func TestStartSpinner_AnimatedMode_StartStop(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	// spinnerDisabled is false — animated mode

	ios.StartSpinner("First")
	time.Sleep(10 * time.Millisecond)
	ios.StopSpinner()

	// Start a second spinner after stopping the first
	ios.StartSpinner("Second")
	time.Sleep(10 * time.Millisecond)
	ios.StopSpinner()
	// Should not panic — verifies goroutine cleanup between cycles
}

func TestStartSpinner_LabelUpdate(t *testing.T) {
	ios, _, _, errOut := iostreams.Test()

	ios.SetSpinnerDisabled(true)

	ios.StartSpinner("First")
	ios.StartSpinner("Second")

	output := errOut.String()
	// Both should appear in text fallback mode
	if !strings.Contains(output, "First...") {
		t.Errorf("expected 'First...' in output, got %q", output)
	}
	if !strings.Contains(output, "Second...") {
		t.Errorf("expected 'Second...' in output, got %q", output)
	}
}
