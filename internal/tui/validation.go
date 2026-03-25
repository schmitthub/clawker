package tui

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// renderValidationError renders a validation error message in the standard
// editor error style. All editors use this for consistent error display.
//
// Returns an empty string when msg is empty (no error).
func renderValidationError(msg string) string {
	if msg == "" {
		return ""
	}
	errStyle := iostreams.ErrorStyle
	return errStyle.Render(fmt.Sprintf("! %s", msg))
}
