package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/assert"
)

func TestNewSpinner(t *testing.T) {
	tests := []struct {
		name        string
		spinnerType SpinnerType
		label       string
	}{
		{"dots", SpinnerDots, "Loading"},
		{"line", SpinnerLine, "Processing"},
		{"minidots", SpinnerMiniDots, ""},
		{"jump", SpinnerJump, "Working"},
		{"pulse", SpinnerPulse, "Waiting"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSpinner(tt.spinnerType, tt.label)
			assert.Equal(t, tt.label, s.label)
			assert.NotNil(t, s.spinner)
		})
	}
}

func TestNewDefaultSpinner(t *testing.T) {
	s := NewDefaultSpinner("Loading")
	assert.Equal(t, "Loading", s.label)
	assert.NotNil(t, s.spinner)
}

func TestSpinnerModel_Init(t *testing.T) {
	s := NewDefaultSpinner("Loading")
	cmd := s.Init()
	// Init should return a tick command
	assert.NotNil(t, cmd)
}

func TestSpinnerModel_View(t *testing.T) {
	tests := []struct {
		name  string
		label string
	}{
		{"with label", "Loading"},
		{"empty label", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewDefaultSpinner(tt.label)
			view := s.View()
			// View should contain the spinner frame
			assert.NotEmpty(t, view)
			if tt.label != "" {
				assert.Contains(t, view, tt.label)
			}
		})
	}
}

func TestSpinnerModel_SetLabel(t *testing.T) {
	s := NewDefaultSpinner("Initial")
	s = s.SetLabel("Updated")
	assert.Equal(t, "Updated", s.label)
}

func TestSpinnerModel_SetSpinnerType(t *testing.T) {
	s := NewDefaultSpinner("Loading")
	s = s.SetSpinnerType(SpinnerMoon)
	// Spinner type should be updated
	assert.NotNil(t, s.spinner)
}

func TestSpinnerModel_Update(t *testing.T) {
	s := NewDefaultSpinner("Loading")

	// Simulate a tick message
	msg := spinner.TickMsg{}
	updated, cmd := s.Update(msg)

	// Should return updated model
	assert.NotNil(t, updated)
	// Should return a command to continue ticking
	assert.NotNil(t, cmd)
}

func TestMapSpinnerType(t *testing.T) {
	tests := []struct {
		name       string
		input      SpinnerType
		wantFrames bool // just verify it returns something with frames
	}{
		{"dots", SpinnerDots, true},
		{"line", SpinnerLine, true},
		{"minidots", SpinnerMiniDots, true},
		{"jump", SpinnerJump, true},
		{"pulse", SpinnerPulse, true},
		{"points", SpinnerPoints, true},
		{"globe", SpinnerGlobe, true},
		{"moon", SpinnerMoon, true},
		{"monkey", SpinnerMonkey, true},
		{"unknown", SpinnerType(99), true}, // defaults to Dot
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapSpinnerType(tt.input)
			assert.NotEmpty(t, result.Frames)
		})
	}
}
