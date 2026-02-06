package iostreams

import "fmt"

// PrintSuccess prints a success message to stderr with a checkmark icon.
// With colors: ✓ message
// Without colors: [ok] message
func (ios *IOStreams) PrintSuccess(format string, args ...any) {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(ios.ErrOut, cs.SuccessIconWithColor(msg))
}

// PrintWarning prints a warning message to stderr with an exclamation icon.
// With colors: ! message
// Without colors: [warn] message
func (ios *IOStreams) PrintWarning(format string, args ...any) {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(ios.ErrOut, cs.WarningIconWithColor(msg))
}

// PrintInfo prints an informational message to stderr with an info icon.
// With colors: ℹ message
// Without colors: [info] message
func (ios *IOStreams) PrintInfo(format string, args ...any) {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(ios.ErrOut, cs.InfoIconWithColor(msg))
}

// PrintFailure prints an error message to stderr with an X icon.
// With colors: ✗ message
// Without colors: [error] message
func (ios *IOStreams) PrintFailure(format string, args ...any) {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(ios.ErrOut, cs.FailureIconWithColor(msg))
}

// PrintEmpty prints an empty state message to stderr.
// Format: "No {noun} found." followed by optional hint lines.
func (ios *IOStreams) PrintEmpty(noun string, hints ...string) {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf("No %s found.", noun)
	fmt.Fprintln(ios.ErrOut, cs.Muted(msg))

	for _, hint := range hints {
		fmt.Fprintln(ios.ErrOut, cs.Muted("  "+hint))
	}
}
