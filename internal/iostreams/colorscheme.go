package iostreams

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// ColorScheme provides terminal color formatting using local styles.
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

// Package-level decoration styles (allocated once, reused by Bold/Italic/Underline/Dim).
var (
	boldStyle      = lipgloss.NewStyle().Bold(true)
	italicStyle    = lipgloss.NewStyle().Italic(true)
	underlineStyle = lipgloss.NewStyle().Underline(true)
	dimStyle       = lipgloss.NewStyle().Faint(true)
)

// render applies a lipgloss style if colors are enabled.
func (cs *ColorScheme) render(style lipgloss.Style, s string) string {
	if !cs.enabled {
		return s
	}
	return style.Render(s)
}

// --- Concrete colors — specific visual effects ---

// Red returns the string in red (error color).
func (cs *ColorScheme) Red(s string) string {
	return cs.render(ErrorStyle, s)
}

// Redf returns a formatted string in red.
func (cs *ColorScheme) Redf(format string, a ...any) string {
	return cs.Red(fmt.Sprintf(format, a...))
}

// Yellow returns the string in yellow (warning color).
func (cs *ColorScheme) Yellow(s string) string {
	return cs.render(WarningStyle, s)
}

// Yellowf returns a formatted string in yellow.
func (cs *ColorScheme) Yellowf(format string, a ...any) string {
	return cs.Yellow(fmt.Sprintf(format, a...))
}

// Green returns the string in green (success color).
func (cs *ColorScheme) Green(s string) string {
	return cs.render(SuccessStyle, s)
}

// Greenf returns a formatted string in green.
func (cs *ColorScheme) Greenf(format string, a ...any) string {
	return cs.Green(fmt.Sprintf(format, a...))
}

// Blue returns the string in blue (primary color, no bold).
func (cs *ColorScheme) Blue(s string) string {
	return cs.render(BlueStyle, s)
}

// Bluef returns a formatted string in blue.
func (cs *ColorScheme) Bluef(format string, a ...any) string {
	return cs.Blue(fmt.Sprintf(format, a...))
}

// Cyan returns the string in cyan (info color).
func (cs *ColorScheme) Cyan(s string) string {
	return cs.render(StatusInfoStyle, s)
}

// Cyanf returns a formatted string in cyan.
func (cs *ColorScheme) Cyanf(format string, a ...any) string {
	return cs.Cyan(fmt.Sprintf(format, a...))
}

// Magenta returns the string in magenta (highlight color).
func (cs *ColorScheme) Magenta(s string) string {
	return cs.render(HighlightStyle, s)
}

// Magentaf returns a formatted string in magenta.
func (cs *ColorScheme) Magentaf(format string, a ...any) string {
	return cs.Magenta(fmt.Sprintf(format, a...))
}

// --- Semantic/theme colors — intent-based styling ---

// Primary returns the string in the primary brand color.
func (cs *ColorScheme) Primary(s string) string {
	return cs.render(TitleStyle, s)
}

// Primaryf returns a formatted string in primary color.
func (cs *ColorScheme) Primaryf(format string, a ...any) string {
	return cs.Primary(fmt.Sprintf(format, a...))
}

// Secondary returns the string in the secondary/supporting color.
func (cs *ColorScheme) Secondary(s string) string {
	return cs.render(SubtitleStyle, s)
}

// Secondaryf returns a formatted string in secondary color.
func (cs *ColorScheme) Secondaryf(format string, a ...any) string {
	return cs.Secondary(fmt.Sprintf(format, a...))
}

// Accent returns the string in the accent/emphasis color.
func (cs *ColorScheme) Accent(s string) string {
	return cs.render(AccentStyle, s)
}

// Accentf returns a formatted string in accent color.
func (cs *ColorScheme) Accentf(format string, a ...any) string {
	return cs.Accent(fmt.Sprintf(format, a...))
}

// Success returns the string in the success/positive color.
func (cs *ColorScheme) Success(s string) string {
	return cs.render(SuccessStyle, s)
}

// Successf returns a formatted string in success color.
func (cs *ColorScheme) Successf(format string, a ...any) string {
	return cs.Success(fmt.Sprintf(format, a...))
}

// Warning returns the string in the warning/caution color.
func (cs *ColorScheme) Warning(s string) string {
	return cs.render(WarningStyle, s)
}

// Warningf returns a formatted string in warning color.
func (cs *ColorScheme) Warningf(format string, a ...any) string {
	return cs.Warning(fmt.Sprintf(format, a...))
}

// Error returns the string in the error/negative color.
func (cs *ColorScheme) Error(s string) string {
	return cs.render(ErrorStyle, s)
}

// Errorf returns a formatted string in error color.
func (cs *ColorScheme) Errorf(format string, a ...any) string {
	return cs.Error(fmt.Sprintf(format, a...))
}

// Info returns the string in the informational color.
func (cs *ColorScheme) Info(s string) string {
	return cs.render(StatusInfoStyle, s)
}

// Infof returns a formatted string in info color.
func (cs *ColorScheme) Infof(format string, a ...any) string {
	return cs.Info(fmt.Sprintf(format, a...))
}

// Muted returns the string in muted/gray color.
func (cs *ColorScheme) Muted(s string) string {
	return cs.render(MutedStyle, s)
}

// Mutedf returns a formatted string in muted color.
func (cs *ColorScheme) Mutedf(format string, a ...any) string {
	return cs.Muted(fmt.Sprintf(format, a...))
}

// Highlight returns the string in highlight/attention color.
func (cs *ColorScheme) Highlight(s string) string {
	return cs.render(HighlightStyle, s)
}

// Highlightf returns a formatted string in highlight color.
func (cs *ColorScheme) Highlightf(format string, a ...any) string {
	return cs.Highlight(fmt.Sprintf(format, a...))
}

// Disabled returns the string in disabled/inactive color.
func (cs *ColorScheme) Disabled(s string) string {
	return cs.render(DisabledStyle, s)
}

// Disabledf returns a formatted string in disabled color.
func (cs *ColorScheme) Disabledf(format string, a ...any) string {
	return cs.Disabled(fmt.Sprintf(format, a...))
}

// --- Text decorations ---

// Bold returns the string in bold.
func (cs *ColorScheme) Bold(s string) string {
	return cs.render(boldStyle, s)
}

// Boldf returns a formatted string in bold.
func (cs *ColorScheme) Boldf(format string, a ...any) string {
	return cs.Bold(fmt.Sprintf(format, a...))
}

// Italic returns the string in italic.
func (cs *ColorScheme) Italic(s string) string {
	return cs.render(italicStyle, s)
}

// Italicf returns a formatted string in italic.
func (cs *ColorScheme) Italicf(format string, a ...any) string {
	return cs.Italic(fmt.Sprintf(format, a...))
}

// Underline returns the string underlined.
func (cs *ColorScheme) Underline(s string) string {
	return cs.render(underlineStyle, s)
}

// Underlinef returns a formatted string underlined.
func (cs *ColorScheme) Underlinef(format string, a ...any) string {
	return cs.Underline(fmt.Sprintf(format, a...))
}

// Dim returns the string in dim/faint style.
func (cs *ColorScheme) Dim(s string) string {
	return cs.render(dimStyle, s)
}

// Dimf returns a formatted string in dim style.
func (cs *ColorScheme) Dimf(format string, a ...any) string {
	return cs.Dim(fmt.Sprintf(format, a...))
}

// --- Icons ---

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
