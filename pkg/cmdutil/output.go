package cmdutil

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/engine"
)

// HandleError prints an error to stderr with user-friendly formatting.
// If the error is a DockerError, it uses FormatUserError for rich output.
// Otherwise, it prints a simple error message.
func HandleError(err error) {
	if err == nil {
		return
	}

	if dockerErr, ok := err.(*engine.DockerError); ok {
		fmt.Fprint(os.Stderr, dockerErr.FormatUserError())
		return
	}

	fmt.Fprintf(os.Stderr, "Error: %s\n", err)
}

// PrintNextSteps prints a "Next Steps" section to stderr.
// Use this when you have actionable suggestions for the user.
func PrintNextSteps(steps ...string) {
	if len(steps) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "\nNext Steps:")
	for i, step := range steps {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, step)
	}
}

// PrintError prints a simple error message to stderr.
// Use HandleError instead when the error might be a DockerError.
func PrintError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

// PrintWarning prints a warning message to stderr.
func PrintWarning(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
}

// PrintStatus prints a status message to stderr unless quiet is enabled.
// Use this for informational messages that can be suppressed with --quiet.
func PrintStatus(quiet bool, format string, args ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// OutputJSON marshals data to stdout as JSON with indentation.
// Use this for machine-readable output when --json flag is set.
func OutputJSON(data any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}
