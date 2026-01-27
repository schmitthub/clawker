package iostreams

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/schmitthub/clawker/internal/tui"
)

// ColorScheme provides terminal color formatting that bridges to tui/styles.go.
// When colors are disabled, methods return the input string unmodified.
type ColorScheme struct {
	enabled bool
	theme   string // "light", "dark", "none"
}

// NewColorScheme creates a new ColorScheme.
// If enabled is false, all color methods return unmodified strings.
// Theme can be "light", "dark", or "none" (affects some color choices).
func NewColorScheme(enabled bool, theme string) *ColorScheme {
	if theme == "" {
		theme = "dark" // Default to dark theme
	}
	return &ColorScheme{
		enabled: enabled,
		theme:   theme,
	}
}

// Enabled returns whether colors are enabled.
func (cs *ColorScheme) Enabled() bool {
	return cs.enabled
}

// Theme returns the current terminal theme.
func (cs *ColorScheme) Theme() string {
	return cs.theme
}

// render applies a lipgloss style if colors are enabled.
func (cs *ColorScheme) render(style lipgloss.Style, s string) string {
	if !cs.enabled {
		return s
	}
	return style.Render(s)
}

// Red returns the string in red (error color).
func (cs *ColorScheme) Red(s string) string {
	return cs.render(tui.ErrorStyle, s)
}

// Redf returns a formatted string in red.
func (cs *ColorScheme) Redf(format string, a ...any) string {
	return cs.Red(fmt.Sprintf(format, a...))
}

// Yellow returns the string in yellow (warning color).
func (cs *ColorScheme) Yellow(s string) string {
	return cs.render(tui.WarningStyle, s)
}

// Yellowf returns a formatted string in yellow.
func (cs *ColorScheme) Yellowf(format string, a ...any) string {
	return cs.Yellow(fmt.Sprintf(format, a...))
}

// Green returns the string in green (success color).
func (cs *ColorScheme) Green(s string) string {
	return cs.render(tui.SuccessStyle, s)
}

// Greenf returns a formatted string in green.
func (cs *ColorScheme) Greenf(format string, a ...any) string {
	return cs.Green(fmt.Sprintf(format, a...))
}

// Blue returns the string in blue (primary color).
func (cs *ColorScheme) Blue(s string) string {
	return cs.render(tui.TitleStyle, s)
}

// Bluef returns a formatted string in blue.
func (cs *ColorScheme) Bluef(format string, a ...any) string {
	return cs.Blue(fmt.Sprintf(format, a...))
}

// Cyan returns the string in cyan (info color).
func (cs *ColorScheme) Cyan(s string) string {
	return cs.render(tui.StatusInfoStyle, s)
}

// Cyanf returns a formatted string in cyan.
func (cs *ColorScheme) Cyanf(format string, a ...any) string {
	return cs.Cyan(fmt.Sprintf(format, a...))
}

// Magenta returns the string in magenta (highlight color).
func (cs *ColorScheme) Magenta(s string) string {
	return cs.render(tui.HighlightStyle, s)
}

// Magentaf returns a formatted string in magenta.
func (cs *ColorScheme) Magentaf(format string, a ...any) string {
	return cs.Magenta(fmt.Sprintf(format, a...))
}

// Bold returns the string in bold.
func (cs *ColorScheme) Bold(s string) string {
	if !cs.enabled {
		return s
	}
	return lipgloss.NewStyle().Bold(true).Render(s)
}

// Boldf returns a formatted string in bold.
func (cs *ColorScheme) Boldf(format string, a ...any) string {
	return cs.Bold(fmt.Sprintf(format, a...))
}

// Muted returns the string in muted/gray color.
func (cs *ColorScheme) Muted(s string) string {
	return cs.render(tui.MutedStyle, s)
}

// Mutedf returns a formatted string in muted color.
func (cs *ColorScheme) Mutedf(format string, a ...any) string {
	return cs.Muted(fmt.Sprintf(format, a...))
}

// SuccessIcon returns a success indicator.
// With colors: green ✓
// Without colors: [ok]
func (cs *ColorScheme) SuccessIcon() string {
	if cs.enabled {
		return cs.Green("✓")
	}
	return "[ok]"
}

// SuccessIconWithColor returns a success indicator with custom text.
func (cs *ColorScheme) SuccessIconWithColor(text string) string {
	if cs.enabled {
		return cs.Green("✓ " + text)
	}
	return "[ok] " + text
}

// WarningIcon returns a warning indicator.
// With colors: yellow !
// Without colors: [warn]
func (cs *ColorScheme) WarningIcon() string {
	if cs.enabled {
		return cs.Yellow("!")
	}
	return "[warn]"
}

// WarningIconWithColor returns a warning indicator with custom text.
func (cs *ColorScheme) WarningIconWithColor(text string) string {
	if cs.enabled {
		return cs.Yellow("! " + text)
	}
	return "[warn] " + text
}

// FailureIcon returns a failure indicator.
// With colors: red ✗
// Without colors: [error]
func (cs *ColorScheme) FailureIcon() string {
	if cs.enabled {
		return cs.Red("✗")
	}
	return "[error]"
}

// FailureIconWithColor returns a failure indicator with custom text.
func (cs *ColorScheme) FailureIconWithColor(text string) string {
	if cs.enabled {
		return cs.Red("✗ " + text)
	}
	return "[error] " + text
}

// InfoIcon returns an info indicator.
// With colors: cyan ℹ
// Without colors: [info]
func (cs *ColorScheme) InfoIcon() string {
	if cs.enabled {
		return cs.Cyan("ℹ")
	}
	return "[info]"
}

// InfoIconWithColor returns an info indicator with custom text.
func (cs *ColorScheme) InfoIconWithColor(text string) string {
	if cs.enabled {
		return cs.Cyan("ℹ " + text)
	}
	return "[info] " + text
}
