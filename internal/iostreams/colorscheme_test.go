package iostreams

import (
	"strings"
	"testing"
)

func TestColorScheme_New(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		theme   string
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

func TestColorScheme_ConcreteColorMethods_Disabled(t *testing.T) {
	tests := []struct {
		name   string
		method func(*ColorScheme, string) string
		input  string
	}{
		{"Red", (*ColorScheme).Red, "error"},
		{"Green", (*ColorScheme).Green, "success"},
		{"Yellow", (*ColorScheme).Yellow, "warning"},
		{"Blue", (*ColorScheme).Blue, "info"},
		{"Cyan", (*ColorScheme).Cyan, "info"},
		{"Magenta", (*ColorScheme).Magenta, "highlight"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(false, "dark")
			result := tt.method(cs, tt.input)
			if result != tt.input {
				t.Errorf("got %q, want %q (unchanged when disabled)", result, tt.input)
			}
		})
	}
}

func TestColorScheme_ConcreteColorMethods_ContainInput(t *testing.T) {
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

func TestColorScheme_ConcreteColorFormatMethods(t *testing.T) {
	cs := NewColorScheme(false, "dark")

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
}

func TestColorScheme_SemanticColorMethods_Disabled(t *testing.T) {
	tests := []struct {
		name   string
		method func(*ColorScheme, string) string
		input  string
	}{
		{"Primary", (*ColorScheme).Primary, "main"},
		{"Secondary", (*ColorScheme).Secondary, "sub"},
		{"Accent", (*ColorScheme).Accent, "emphasis"},
		{"Success", (*ColorScheme).Success, "ok"},
		{"Warning", (*ColorScheme).Warning, "caution"},
		{"Error", (*ColorScheme).Error, "fail"},
		{"Info", (*ColorScheme).Info, "note"},
		{"Muted", (*ColorScheme).Muted, "dim"},
		{"Highlight", (*ColorScheme).Highlight, "bright"},
		{"Disabled", (*ColorScheme).Disabled, "inactive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(false, "dark")
			result := tt.method(cs, tt.input)
			if result != tt.input {
				t.Errorf("got %q, want %q (unchanged when disabled)", result, tt.input)
			}
		})
	}
}

func TestColorScheme_SemanticColorMethods_ContainInput(t *testing.T) {
	cs := NewColorScheme(true, "dark")

	methods := []struct {
		name   string
		method func(*ColorScheme, string) string
	}{
		{"Primary", (*ColorScheme).Primary},
		{"Secondary", (*ColorScheme).Secondary},
		{"Accent", (*ColorScheme).Accent},
		{"Success", (*ColorScheme).Success},
		{"Warning", (*ColorScheme).Warning},
		{"Error", (*ColorScheme).Error},
		{"Info", (*ColorScheme).Info},
		{"Muted", (*ColorScheme).Muted},
		{"Highlight", (*ColorScheme).Highlight},
		{"Disabled", (*ColorScheme).Disabled},
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

func TestColorScheme_SemanticColorFormatMethods(t *testing.T) {
	cs := NewColorScheme(false, "dark")

	if got := cs.Primaryf("main: %d", 1); got != "main: 1" {
		t.Errorf("Primaryf() = %q, want %q", got, "main: 1")
	}
	if got := cs.Secondaryf("sub: %s", "text"); got != "sub: text" {
		t.Errorf("Secondaryf() = %q, want %q", got, "sub: text")
	}
	if got := cs.Accentf("accent: %s", "text"); got != "accent: text" {
		t.Errorf("Accentf() = %q, want %q", got, "accent: text")
	}
	if got := cs.Successf("ok: %d", 1); got != "ok: 1" {
		t.Errorf("Successf() = %q, want %q", got, "ok: 1")
	}
	if got := cs.Warningf("warn: %s", "x"); got != "warn: x" {
		t.Errorf("Warningf() = %q, want %q", got, "warn: x")
	}
	if got := cs.Errorf("err: %s", "x"); got != "err: x" {
		t.Errorf("Errorf() = %q, want %q", got, "err: x")
	}
	if got := cs.Infof("info: %s", "x"); got != "info: x" {
		t.Errorf("Infof() = %q, want %q", got, "info: x")
	}
	if got := cs.Mutedf("muted: %s", "x"); got != "muted: x" {
		t.Errorf("Mutedf() = %q, want %q", got, "muted: x")
	}
	if got := cs.Highlightf("hl: %s", "x"); got != "hl: x" {
		t.Errorf("Highlightf() = %q, want %q", got, "hl: x")
	}
	if got := cs.Disabledf("dis: %s", "x"); got != "dis: x" {
		t.Errorf("Disabledf() = %q, want %q", got, "dis: x")
	}
}

func TestColorScheme_TextDecorations_Disabled(t *testing.T) {
	tests := []struct {
		name   string
		method func(*ColorScheme, string) string
		input  string
	}{
		{"Bold", (*ColorScheme).Bold, "important"},
		{"Italic", (*ColorScheme).Italic, "emphasis"},
		{"Underline", (*ColorScheme).Underline, "link"},
		{"Dim", (*ColorScheme).Dim, "faint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewColorScheme(false, "dark")
			result := tt.method(cs, tt.input)
			if result != tt.input {
				t.Errorf("got %q, want %q (unchanged when disabled)", result, tt.input)
			}
		})
	}
}

func TestColorScheme_TextDecorations_ContainInput(t *testing.T) {
	cs := NewColorScheme(true, "dark")

	methods := []struct {
		name   string
		method func(*ColorScheme, string) string
	}{
		{"Bold", (*ColorScheme).Bold},
		{"Italic", (*ColorScheme).Italic},
		{"Underline", (*ColorScheme).Underline},
		{"Dim", (*ColorScheme).Dim},
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

func TestColorScheme_TextDecorationFormatMethods(t *testing.T) {
	cs := NewColorScheme(false, "dark")

	if got := cs.Boldf("bold: %s", "str"); got != "bold: str" {
		t.Errorf("Boldf() = %q, want %q", got, "bold: str")
	}
	if got := cs.Italicf("italic: %s", "str"); got != "italic: str" {
		t.Errorf("Italicf() = %q, want %q", got, "italic: str")
	}
	if got := cs.Underlinef("underline: %s", "str"); got != "underline: str" {
		t.Errorf("Underlinef() = %q, want %q", got, "underline: str")
	}
	if got := cs.Dimf("dim: %s", "str"); got != "dim: str" {
		t.Errorf("Dimf() = %q, want %q", got, "dim: str")
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

func TestColorScheme_IconsWithText_Disabled(t *testing.T) {
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

func TestColorScheme_IconsWithText_Enabled(t *testing.T) {
	cs := NewColorScheme(true, "dark")

	if got := cs.SuccessIconWithColor("done"); !strings.Contains(got, "done") || !strings.Contains(got, "✓") {
		t.Errorf("SuccessIconWithColor() = %q, want to contain both ✓ and 'done'", got)
	}
	if got := cs.WarningIconWithColor("caution"); !strings.Contains(got, "caution") || !strings.Contains(got, "!") {
		t.Errorf("WarningIconWithColor() = %q, want to contain both ! and 'caution'", got)
	}
	if got := cs.FailureIconWithColor("failed"); !strings.Contains(got, "failed") || !strings.Contains(got, "✗") {
		t.Errorf("FailureIconWithColor() = %q, want to contain both ✗ and 'failed'", got)
	}
	if got := cs.InfoIconWithColor("note"); !strings.Contains(got, "note") || !strings.Contains(got, "ℹ") {
		t.Errorf("InfoIconWithColor() = %q, want to contain both ℹ and 'note'", got)
	}
}
