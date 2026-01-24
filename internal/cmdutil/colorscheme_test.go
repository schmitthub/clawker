package cmdutil

import (
	"strings"
	"testing"
)

func TestColorScheme_New(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		theme   string
		wantErr bool
	}{
		{
			name:    "enabled with dark theme",
			enabled: true,
			theme:   "dark",
		},
		{
			name:    "enabled with light theme",
			enabled: true,
			theme:   "light",
		},
		{
			name:    "disabled",
			enabled: false,
			theme:   "dark",
		},
		{
			name:    "empty theme defaults to dark",
			enabled: true,
			theme:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(tt.enabled, tt.theme)
			if cs == nil {
				t.Fatal("NewColorScheme returned nil")
			}
			if cs.Enabled() != tt.enabled {
				t.Errorf("Enabled() = %v, want %v", cs.Enabled(), tt.enabled)
			}
			expectedTheme := tt.theme
			if expectedTheme == "" {
				expectedTheme = "dark"
			}
			if cs.Theme() != expectedTheme {
				t.Errorf("Theme() = %v, want %v", cs.Theme(), expectedTheme)
			}
		})
	}
}

func TestColorScheme_ColorMethods(t *testing.T) {
	tests := []struct {
		name     string
		method   func(*ColorScheme, string) string
		input    string
		enabled  bool
		wantSame bool // If true, expect unchanged string when disabled
	}{
		{
			name:     "Red disabled",
			method:   (*ColorScheme).Red,
			input:    "error",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Green disabled",
			method:   (*ColorScheme).Green,
			input:    "success",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Yellow disabled",
			method:   (*ColorScheme).Yellow,
			input:    "warning",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Blue disabled",
			method:   (*ColorScheme).Blue,
			input:    "info",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Cyan disabled",
			method:   (*ColorScheme).Cyan,
			input:    "info",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Magenta disabled",
			method:   (*ColorScheme).Magenta,
			input:    "highlight",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Bold disabled",
			method:   (*ColorScheme).Bold,
			input:    "important",
			enabled:  false,
			wantSame: true,
		},
		{
			name:     "Muted disabled",
			method:   (*ColorScheme).Muted,
			input:    "secondary",
			enabled:  false,
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(tt.enabled, "dark")
			result := tt.method(cs, tt.input)

			if tt.wantSame {
				if result != tt.input {
					t.Errorf("got %q, want %q (unchanged)", result, tt.input)
				}
			} else {
				// When enabled, result should contain the input
				// Note: lipgloss may not add ANSI codes if there's no TTY
				if !strings.Contains(result, tt.input) {
					t.Errorf("result %q does not contain input %q", result, tt.input)
				}
			}
		})
	}
}

func TestColorScheme_ColorMethodsContainInput(t *testing.T) {
	// Test that when enabled, output at minimum contains the input
	// (ANSI codes depend on TTY detection by lipgloss)
	cs := NewColorScheme(true, "dark")

	methods := []struct {
		name   string
		method func(*ColorScheme, string) string
	}{
		{"Red", (*ColorScheme).Red},
		{"Green", (*ColorScheme).Green},
		{"Yellow", (*ColorScheme).Yellow},
		{"Blue", (*ColorScheme).Blue},
		{"Cyan", (*ColorScheme).Cyan},
		{"Magenta", (*ColorScheme).Magenta},
		{"Bold", (*ColorScheme).Bold},
		{"Muted", (*ColorScheme).Muted},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			input := "test-string"
			result := m.method(cs, input)
			if !strings.Contains(result, input) {
				t.Errorf("%s(%q) = %q, does not contain input", m.name, input, result)
			}
		})
	}
}

func TestColorScheme_FormatMethods(t *testing.T) {
	cs := NewColorScheme(false, "dark")

	// Test format methods return formatted strings when disabled
	if got := cs.Redf("error: %d", 42); got != "error: 42" {
		t.Errorf("Redf() = %q, want %q", got, "error: 42")
	}
	if got := cs.Greenf("count: %d", 10); got != "count: 10" {
		t.Errorf("Greenf() = %q, want %q", got, "count: 10")
	}
	if got := cs.Yellowf("warn: %s", "test"); got != "warn: test" {
		t.Errorf("Yellowf() = %q, want %q", got, "warn: test")
	}
	if got := cs.Bluef("info: %s", "data"); got != "info: data" {
		t.Errorf("Bluef() = %q, want %q", got, "info: data")
	}
	if got := cs.Cyanf("log: %s", "msg"); got != "log: msg" {
		t.Errorf("Cyanf() = %q, want %q", got, "log: msg")
	}
	if got := cs.Magentaf("hl: %s", "text"); got != "hl: text" {
		t.Errorf("Magentaf() = %q, want %q", got, "hl: text")
	}
	if got := cs.Boldf("bold: %s", "str"); got != "bold: str" {
		t.Errorf("Boldf() = %q, want %q", got, "bold: str")
	}
	if got := cs.Mutedf("muted: %s", "val"); got != "muted: val" {
		t.Errorf("Mutedf() = %q, want %q", got, "muted: val")
	}
}

func TestColorScheme_Icons(t *testing.T) {
	tests := []struct {
		name          string
		enabled       bool
		wantSuccess   string
		wantWarning   string
		wantFailure   string
		wantInfo      string
		containsEmoji bool
	}{
		{
			name:          "icons enabled",
			enabled:       true,
			containsEmoji: true,
		},
		{
			name:        "icons disabled",
			enabled:     false,
			wantSuccess: "[ok]",
			wantWarning: "[warn]",
			wantFailure: "[error]",
			wantInfo:    "[info]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(tt.enabled, "dark")

			if tt.containsEmoji {
				// When enabled, icons should contain special characters
				if !strings.Contains(cs.SuccessIcon(), "✓") {
					t.Error("SuccessIcon should contain ✓ when enabled")
				}
				if !strings.Contains(cs.WarningIcon(), "!") {
					t.Error("WarningIcon should contain ! when enabled")
				}
				if !strings.Contains(cs.FailureIcon(), "✗") {
					t.Error("FailureIcon should contain ✗ when enabled")
				}
				if !strings.Contains(cs.InfoIcon(), "ℹ") {
					t.Error("InfoIcon should contain ℹ when enabled")
				}
			} else {
				if cs.SuccessIcon() != tt.wantSuccess {
					t.Errorf("SuccessIcon() = %q, want %q", cs.SuccessIcon(), tt.wantSuccess)
				}
				if cs.WarningIcon() != tt.wantWarning {
					t.Errorf("WarningIcon() = %q, want %q", cs.WarningIcon(), tt.wantWarning)
				}
				if cs.FailureIcon() != tt.wantFailure {
					t.Errorf("FailureIcon() = %q, want %q", cs.FailureIcon(), tt.wantFailure)
				}
				if cs.InfoIcon() != tt.wantInfo {
					t.Errorf("InfoIcon() = %q, want %q", cs.InfoIcon(), tt.wantInfo)
				}
			}
		})
	}
}

func TestColorScheme_IconsWithText(t *testing.T) {
	cs := NewColorScheme(false, "dark")

	if got := cs.SuccessIconWithColor("done"); got != "[ok] done" {
		t.Errorf("SuccessIconWithColor() = %q, want %q", got, "[ok] done")
	}
	if got := cs.WarningIconWithColor("caution"); got != "[warn] caution" {
		t.Errorf("WarningIconWithColor() = %q, want %q", got, "[warn] caution")
	}
	if got := cs.FailureIconWithColor("failed"); got != "[error] failed" {
		t.Errorf("FailureIconWithColor() = %q, want %q", got, "[error] failed")
	}
	if got := cs.InfoIconWithColor("note"); got != "[info] note" {
		t.Errorf("InfoIconWithColor() = %q, want %q", got, "[info] note")
	}
}
