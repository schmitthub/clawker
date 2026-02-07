package cmdutil

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// userFormattedError is a duck-typed interface for errors that can format
// themselves for user display. docker.DockerError satisfies this interface.
type userFormattedError interface {
	FormatUserError() string
}

// Deprecated: Use fmt.Fprintf(ios.ErrOut, ...) with ios.ColorScheme() directly.
// Errors should be returned to Main() for centralized rendering via printError.
// This function will be removed once all commands are migrated.
func HandleError(ios *iostreams.IOStreams, err error) {
	if err == nil {
		return
	}

	var ufErr userFormattedError
	if errors.As(err, &ufErr) {
		fmt.Fprint(ios.ErrOut, ufErr.FormatUserError())
		return
	}

	fmt.Fprintf(ios.ErrOut, "Error: %s\n", err)
}

// Deprecated: Inline the next-steps output in the command's run function.
// Use fmt.Fprintf(ios.ErrOut, ...) with ios.ColorScheme() directly.
// This function will be removed once all commands are migrated.
func PrintNextSteps(ios *iostreams.IOStreams, steps ...string) {
	if len(steps) == 0 {
		return
	}

	fmt.Fprintln(ios.ErrOut, "\nNext Steps:")
	for i, step := range steps {
		fmt.Fprintf(ios.ErrOut, "  %d. %s\n", i+1, step)
	}
}

// Deprecated: Use fmt.Fprintf(ios.ErrOut, "Error: "+format+"\n", args...) directly.
// Errors should be returned to Main() for centralized rendering.
// This function will be removed once all commands are migrated.
func PrintError(ios *iostreams.IOStreams, format string, args ...any) {
	fmt.Fprintf(ios.ErrOut, "Error: "+format+"\n", args...)
}

// Deprecated: Use fmt.Fprintf(ios.ErrOut, "%s "+format+"\n", cs.WarningIcon(), args...) directly.
// This function will be removed once all commands are migrated.
func PrintWarning(ios *iostreams.IOStreams, format string, args ...any) {
	fmt.Fprintf(ios.ErrOut, "Warning: "+format+"\n", args...)
}

// PrintStatus prints a status message to stderr unless quiet is enabled.
// Use this for informational messages that can be suppressed with --quiet.
func PrintStatus(ios *iostreams.IOStreams, quiet bool, format string, args ...any) {
	if !quiet {
		fmt.Fprintf(ios.ErrOut, format+"\n", args...)
	}
}

// OutputJSON marshals data to stdout as JSON with indentation.
// Use this for machine-readable output when --json flag is set.
func OutputJSON(ios *iostreams.IOStreams, data any) error {
	enc := json.NewEncoder(ios.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// PrintHelpHint prints a contextual help hint to stderr.
// cmdPath should be cmd.CommandPath() (e.g., "clawker container stop")
func PrintHelpHint(ios *iostreams.IOStreams, cmdPath string) {
	fmt.Fprintf(ios.ErrOut, "\nRun '%s --help' for more information.\n", cmdPath)
}
