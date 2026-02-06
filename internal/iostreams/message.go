package iostreams

import "fmt"

// PrintSuccess prints a success message to stderr with a checkmark icon.
// With colors: ✓ message
// Without colors: [ok] message
func (ios *IOStreams) PrintSuccess(format string, args ...any) error {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	_, err := fmt.Fprintln(ios.ErrOut, cs.SuccessIconWithColor(msg))
	return err
}

// PrintWarning prints a warning message to stderr with an exclamation icon.
// With colors: ! message
// Without colors: [warn] message
func (ios *IOStreams) PrintWarning(format string, args ...any) error {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	_, err := fmt.Fprintln(ios.ErrOut, cs.WarningIconWithColor(msg))
	return err
}

// PrintInfo prints an informational message to stderr with an info icon.
// With colors: ℹ message
// Without colors: [info] message
func (ios *IOStreams) PrintInfo(format string, args ...any) error {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	_, err := fmt.Fprintln(ios.ErrOut, cs.InfoIconWithColor(msg))
	return err
}

// PrintFailure prints an error message to stderr with an X icon.
// With colors: ✗ message
// Without colors: [error] message
func (ios *IOStreams) PrintFailure(format string, args ...any) error {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf(format, args...)
	_, err := fmt.Fprintln(ios.ErrOut, cs.FailureIconWithColor(msg))
	return err
}

// PrintEmpty prints an empty state message to stderr.
// Format: "No {noun} found." followed by optional hint lines.
func (ios *IOStreams) PrintEmpty(noun string, hints ...string) error {
	cs := ios.ColorScheme()
	msg := fmt.Sprintf("No %s found.", noun)
	if _, err := fmt.Fprintln(ios.ErrOut, cs.Muted(msg)); err != nil {
		return err
	}

	for _, hint := range hints {
		if _, err := fmt.Fprintln(ios.ErrOut, cs.Muted("  "+hint)); err != nil {
			return err
		}
	}
	return nil
}
