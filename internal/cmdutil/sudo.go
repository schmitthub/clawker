package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// sudoPrompt is what the credential prompt asks for. Callers suppress sudo's
// own prompt (-p ”), so this is the only thing the person sees.
const sudoPrompt = "[sudo] password"

// helperFileMode is the mode a staged helper binary is written with. The
// other-execute bit is load-bearing: a helper that re-execs itself inside a
// fresh user namespace (idmap-mount's holder child) does so before any
// uid_map is written, so its owner is unmapped there, capability overrides
// don't apply to unmapped-owner inodes, and the DAC check falls through to
// the "other" bits. The 0700 staging directory still keeps the path private;
// /proc/self/exe is a magic link, so directory modes never gate the re-exec.
const helperFileMode = 0o755

// ErrSudoUnavailable reports that the elevated step cannot even be attempted
// on this machine, so callers can say so rather than reporting a failure.
var ErrSudoUnavailable = errors.New("sudo is not on PATH")

// ElevatedHelper describes one privileged one-shot: an embedded binary, the
// name it is staged under (which is what the person sees in the sudo prompt
// and the audit log, so it should say what it does), and its arguments.
type ElevatedHelper struct {
	Name   string
	Binary []byte
	Args   []string
}

// RunElevated stages an embedded helper into a fresh private directory, asks
// for the sudo credential, runs the helper once under sudo, and removes it.
//
// The helper is embedded rather than installed: nothing is left on the user's
// system and nothing about clawker's install has to change for a host that
// needs one of these steps. The directory is fresh rather than a fixed path
// in the temp root because the binary is about to run as root, and a fixed
// path in a world-writable directory is a name another local user can win a
// race for.
//
// Callers are responsible for deciding that elevation is warranted at all —
// this runs the prompt as soon as it is called.
func RunElevated(ctx context.Context, ios *iostreams.IOStreams, helper ElevatedHelper) error {
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		return ErrSudoUnavailable
	}

	helperPath, cleanup, err := stageHelper(helper)
	if err != nil {
		return err
	}
	defer cleanup()

	// Callers commonly run this behind a spinner, which renders to the same
	// stream the prompt is about to land on. Stopping it is a no-op when
	// nothing is spinning, and the caller's own deferred stop stays safe.
	ios.StopSpinner()

	credential, err := SudoPassword(ios)
	if err != nil {
		return err
	}

	// -S reads the credential from stdin and -p silences sudo's own prompt,
	// which was already asked above. Helpers read nothing from stdin, so what
	// they inherit is a reader sudo has already drained.
	args := append([]string{"-S", "-p", "", helperPath}, helper.Args...)
	cmd := exec.CommandContext(ctx, sudoPath, args...)
	cmd.Stdin = strings.NewReader(credential + "\n")
	cmd.Stdout, cmd.Stderr = ios.Out, ios.ErrOut
	if runErr := cmd.Run(); runErr != nil {
		return fmt.Errorf("the elevated helper failed: %w", runErr)
	}
	return nil
}

// stageHelper writes the embedded binary to a fresh 0700 directory and
// returns its path plus a cleanup.
func stageHelper(helper ElevatedHelper) (string, func(), error) {
	dir, err := os.MkdirTemp("", consts.NamePrefix+"-elevated-")
	if err != nil {
		return "", nil, fmt.Errorf("creating a directory for the elevated helper: %w", err)
	}
	cleanup := func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			// Nothing actionable: the helper has already run or failed, and
			// the leftover is a temp directory the OS reclaims.
			_ = rerr
		}
	}

	path := filepath.Join(dir, helper.Name)
	if werr := os.WriteFile(path, helper.Binary, helperFileMode); werr != nil {
		cleanup()
		return "", nil, fmt.Errorf("writing the elevated helper: %w", werr)
	}
	return path, cleanup, nil
}

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
