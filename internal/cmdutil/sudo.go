package cmdutil

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// sudoPrompt is what the credential prompt asks for. Callers suppress sudo's
// own prompt (-p ”), so this is the only thing the person sees.
const sudoPrompt = "[sudo] password"

// SudoPassword prompts for the invoking user's sudo credential without echo
// and returns it. It runs nothing itself — the caller feeds the credential to
// `sudo -S` on stdin. A non-interactive session errors instead of reading
// cleartext from a pipe.
func SudoPassword(ios *iostreams.IOStreams) (string, error) {
	credential, err := prompter.NewPrompter(ios).Password(sudoPrompt)
	if err != nil {
		return "", fmt.Errorf("prompting for the sudo credential: %w", err)
	}
	return credential, nil
}
