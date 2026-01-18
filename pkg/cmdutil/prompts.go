package cmdutil

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// PromptForConfirmation prompts the user for y/N confirmation.
// Uses the provided reader for input (pass cmd.InOrStdin() for testability).
// Returns true only for "y" or "Y", false for anything else including errors.
// Writes prompt to stderr (not stdout) to preserve stdout for data output.
func PromptForConfirmation(in io.Reader, message string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", message)
	reader := bufio.NewReader(in)
	response, err := reader.ReadString('\n')
	if err != nil {
		// Treat read errors (EOF, etc.) as "no"
		fmt.Fprintln(os.Stderr) // Newline for cleaner output
		return false
	}
	response = strings.TrimSpace(response)
	return response == "y" || response == "Y"
}
