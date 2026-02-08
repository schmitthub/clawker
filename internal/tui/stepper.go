package tui

import (
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/iostreams"
	cltext "github.com/schmitthub/clawker/internal/text"
)

// StepState represents the state of a step in a stepper bar.
type StepState int

const (
	// StepPendingState indicates the step has not been started.
	StepPendingState StepState = iota
	// StepActiveState indicates the step is currently active.
	StepActiveState
	// StepCompleteState indicates the step has been completed.
	StepCompleteState
	// StepSkippedState indicates the step was skipped (hidden from display).
	StepSkippedState
)

// Step represents a single step in a stepper bar.
type Step struct {
	Title string    // Short label for the bar
	Value string    // Displayed next to completed steps (e.g., "bookworm")
	State StepState
}

// RenderStepperBar renders a horizontal step indicator.
//
// Completed steps show a checkmark icon with their title and optional value.
// Active steps show a filled circle icon with their title.
// Pending steps show an empty circle icon with their title.
// Skipped steps are hidden entirely.
//
// Example output:
//
//	✓ Build Image: Yes  →  ◉ Flavor  →  ○ Submit
func RenderStepperBar(steps []Step, width int) string {
	var parts []string

	for _, step := range steps {
		if step.State == StepSkippedState {
			continue
		}

		var segment string
		switch step.State {
		case StepCompleteState:
			icon := iostreams.SuccessStyle.Render("✓")
			if step.Value != "" {
				segment = fmt.Sprintf("%s %s: %s", icon, step.Title, step.Value)
			} else {
				segment = fmt.Sprintf("%s %s", icon, step.Title)
			}
		case StepActiveState:
			icon := iostreams.TitleStyle.Render("◉")
			title := iostreams.TitleStyle.Render(step.Title)
			segment = fmt.Sprintf("%s %s", icon, title)
		case StepPendingState:
			icon := iostreams.MutedStyle.Render("○")
			title := iostreams.MutedStyle.Render(step.Title)
			segment = fmt.Sprintf("%s %s", icon, title)
		}

		parts = append(parts, segment)
	}

	if len(parts) == 0 {
		return ""
	}

	separator := iostreams.MutedStyle.Render(" → ")
	result := strings.Join(parts, separator)

	if width > 0 && cltext.CountVisibleWidth(result) > width {
		result = cltext.Truncate(result, width)
	}

	return result
}
