package cmdutil

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressIndicator_StartStop(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "Loading")
	p.start()

	// Give spinner time to render at least once
	time.Sleep(100 * time.Millisecond)

	p.stop()

	// Output should have contained spinner and label
	output := buf.String()
	if !strings.Contains(output, "Loading") {
		t.Errorf("output should contain label 'Loading', got %q", output)
	}
}

func TestProgressIndicator_StopWithMessage(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "Processing")
	p.start()

	// Give spinner time to render
	time.Sleep(100 * time.Millisecond)

	p.stopWithMessage("Done!")

	// Final output should contain the done message
	output := buf.String()
	if !strings.Contains(output, "Done!") {
		t.Errorf("output should contain final message 'Done!', got %q", output)
	}
}

func TestProgressIndicator_SetLabel(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "Initial")
	p.start()

	// Give spinner time to render with initial label
	time.Sleep(100 * time.Millisecond)

	p.setLabel("Updated")

	// Give spinner time to render with new label
	time.Sleep(100 * time.Millisecond)

	p.stop()

	output := buf.String()
	if !strings.Contains(output, "Updated") {
		t.Errorf("output should contain updated label 'Updated', got %q", output)
	}
}

func TestProgressIndicator_StopTwice(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "Test")
	p.start()
	time.Sleep(50 * time.Millisecond)

	// Stop twice should not panic
	p.stop()
	p.stop()
}

func TestProgressIndicator_StopWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "Test")

	// Stop without start should not panic
	p.stop()
}

func TestProgressIndicator_NoLabel(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressIndicator(&buf, "")
	p.start()
	time.Sleep(100 * time.Millisecond)
	p.stop()

	// Should have spinner frames in output
	output := buf.String()
	hasSpinnerFrame := false
	for _, frame := range spinnerFrames {
		if strings.Contains(output, frame) {
			hasSpinnerFrame = true
			break
		}
	}
	if !hasSpinnerFrame {
		t.Errorf("output should contain at least one spinner frame, got %q", output)
	}
}

func TestSpinnerFrames(t *testing.T) {
	// Verify spinner frames are valid
	if len(spinnerFrames) == 0 {
		t.Fatal("spinnerFrames should not be empty")
	}

	for i, frame := range spinnerFrames {
		if frame == "" {
			t.Errorf("spinnerFrames[%d] should not be empty", i)
		}
	}
}
