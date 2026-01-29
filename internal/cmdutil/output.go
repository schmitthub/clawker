package cmdutil

import (
	"encoding/json"
	"fmt"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// ExitError represents a container exit with a non-zero status code.
// Commands should return this instead of calling os.Exit() directly,
// allowing deferred cleanup to run. The root command handles os.Exit().
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// HandleError prints an error to stderr with user-friendly formatting.
// If the error is a DockerError, it uses FormatUserError for rich output.
// Otherwise, it prints a simple error message.
func HandleError(ios *iostreams.IOStreams, err error) {
	if err == nil {
		return
	}

	if dockerErr, ok := err.(*docker.DockerError); ok {
		fmt.Fprint(ios.ErrOut, dockerErr.FormatUserError())
		return
	}

	fmt.Fprintf(ios.ErrOut, "Error: %s\n", err)
}

// PrintNextSteps prints a "Next Steps" section to stderr.
// Use this when you have actionable suggestions for the user.
func PrintNextSteps(ios *iostreams.IOStreams, steps ...string) {
	if len(steps) == 0 {
		return
	}

	fmt.Fprintln(ios.ErrOut, "\nNext Steps:")
	for i, step := range steps {
		fmt.Fprintf(ios.ErrOut, "  %d. %s\n", i+1, step)
	}
}

// PrintError prints a simple error message to stderr.
// Use HandleError instead when the error might be a DockerError.
func PrintError(ios *iostreams.IOStreams, format string, args ...any) {
	fmt.Fprintf(ios.ErrOut, "Error: "+format+"\n", args...)
}

// PrintWarning prints a warning message to stderr.
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
