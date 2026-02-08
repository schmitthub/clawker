package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/text"
)

func TestStepperBar_Rendering(t *testing.T) {
	steps := []Step{
		{Title: "Build", Value: "Yes", State: StepCompleteState},
		{Title: "Flavor", State: StepActiveState},
		{Title: "Submit", State: StepPendingState},
	}

	result := RenderStepperBar(steps, 120)
	plain := text.StripANSI(result)

	// Verify icons are present for each state.
	assert.Contains(t, plain, "✓", "complete step should show checkmark icon")
	assert.Contains(t, plain, "◉", "active step should show filled circle icon")
	assert.Contains(t, plain, "○", "pending step should show empty circle icon")

	// Verify step titles are present.
	assert.Contains(t, plain, "Build", "complete step title should be rendered")
	assert.Contains(t, plain, "Flavor", "active step title should be rendered")
	assert.Contains(t, plain, "Submit", "pending step title should be rendered")

	// Verify the completed step shows its value.
	assert.Contains(t, plain, "Yes", "completed step should show its value")

	// Verify separators between steps.
	assert.Contains(t, plain, "→", "steps should be separated by arrow")
}

func TestStepperBar_SkippedHidden(t *testing.T) {
	steps := []Step{
		{Title: "First", State: StepCompleteState},
		{Title: "Hidden", State: StepSkippedState},
		{Title: "Last", State: StepActiveState},
	}

	result := RenderStepperBar(steps, 120)
	plain := text.StripANSI(result)

	assert.NotContains(t, plain, "Hidden", "skipped step should not be rendered")
	assert.Contains(t, plain, "First", "non-skipped step should be rendered")
	assert.Contains(t, plain, "Last", "non-skipped step should be rendered")
}

func TestStepperBar_Truncation(t *testing.T) {
	steps := []Step{
		{Title: "Very Long Step Name One", State: StepCompleteState},
		{Title: "Very Long Step Name Two", State: StepActiveState},
		{Title: "Very Long Step Name Three", State: StepPendingState},
	}

	const maxWidth = 30
	result := RenderStepperBar(steps, maxWidth)

	visibleWidth := text.CountVisibleWidth(result)
	assert.LessOrEqual(t, visibleWidth, maxWidth,
		"rendered bar should fit within width constraint; got %d chars", visibleWidth)

	// A truncated result should end with ellipsis.
	plain := text.StripANSI(result)
	assert.Contains(t, plain, "...", "truncated bar should end with ellipsis")
}

func TestStepperBar_Empty(t *testing.T) {
	result := RenderStepperBar(nil, 120)
	assert.Empty(t, result, "empty steps should return empty string")

	result = RenderStepperBar([]Step{}, 120)
	assert.Empty(t, result, "empty slice should return empty string")
}

func TestStepperBar_CompletedWithValue(t *testing.T) {
	steps := []Step{
		{Title: "Distro", Value: "bookworm", State: StepCompleteState},
	}

	result := RenderStepperBar(steps, 120)
	plain := text.StripANSI(result)

	// Completed step should render as "✓ Title: Value".
	assert.Contains(t, plain, "Distro: bookworm",
		"completed step should show title colon value suffix")
}

func TestStepperBar_CompletedWithoutValue(t *testing.T) {
	steps := []Step{
		{Title: "Confirm", State: StepCompleteState},
	}

	result := RenderStepperBar(steps, 120)
	plain := text.StripANSI(result)

	assert.Contains(t, plain, "✓ Confirm",
		"completed step without value should show icon and title only")
	assert.NotContains(t, plain, ":",
		"completed step without value should not contain colon")
}

func TestStepperBar_AllSkipped(t *testing.T) {
	steps := []Step{
		{Title: "A", State: StepSkippedState},
		{Title: "B", State: StepSkippedState},
	}

	result := RenderStepperBar(steps, 120)
	assert.Empty(t, result, "all-skipped steps should return empty string")
}

func TestStepperBar_SingleStep(t *testing.T) {
	steps := []Step{
		{Title: "Only", State: StepActiveState},
	}

	result := RenderStepperBar(steps, 120)
	plain := text.StripANSI(result)

	assert.Contains(t, plain, "◉", "single active step should show filled circle icon")
	assert.Contains(t, plain, "Only", "single step title should be rendered")
	assert.NotContains(t, plain, "→", "single step should have no separator")
}

func TestStepperBar_ZeroWidth(t *testing.T) {
	steps := []Step{
		{Title: "Step", State: StepActiveState},
	}

	// Width of 0 means no truncation (the condition is width > 0).
	result := RenderStepperBar(steps, 0)
	plain := text.StripANSI(result)
	assert.Contains(t, plain, "Step", "zero width should not truncate")
}
