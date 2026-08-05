// Package shared holds the helpers the control plane bootstrap verbs have in
// common — currently the assistance a blocked control plane boot asks for.
package shared

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

const (
	// helperFileMode is the mode of the staged helper binary. Only the
	// invoking user can read or execute it; root ignores the mode entirely.
	helperFileMode = 0o700

	// helperBinaryName is what the staged helper is called on disk. It shows
	// up in the sudo prompt and in the audit log, so it says what it does.
	helperBinaryName = consts.NamePrefix + "-bpffs-delegate"
)

// AssistSOS resolves one control plane SOS, reporting nil only when the
// assistance actually landed. Callers catch the *CPSOSError a Manager.Start
// returns, hand it here, and call Start again on success — Start is
// idempotent, the control plane picks up where it was blocked, and the time a
// human spent typing a password is not charged against the readiness budget.
//
// It dispatches on Kind and never on Message. Kind is a closed enum both sides
// compile against; the message is prose for a human, and an older CLI meeting
// a newer control plane must surface it rather than guess at it. That
// unknown-kind arm returns the SOS itself as the error: the one behaviour this
// must never have is to hang waiting for help that is not coming.
//
// Every arm is allowed to decline. Assistance means running something
// privileged, which is only reasonable with a human present to authorize it —
// so a non-interactive session (a script, CI, a piped invocation) returns the
// error and lets the caller decide, rather than blocking on a prompt nobody
// will ever see.
func AssistSOS(ctx context.Context, sos *cpmanager.CPSOSError, ios *iostreams.IOStreams) error {
	if ios == nil || !ios.CanPrompt() {
		return sos
	}

	switch sos.Kind {
	case adminv1.SOSKind_SOS_KIND_BPFFS_DELEGATION:
		return delegateBPFFS(ctx, sos, ios)
	case adminv1.SOSKind_SOS_KIND_UNSPECIFIED:
		return sos
	default:
		return sos
	}
}

// delegateBPFFS runs the elevated helper that completes BPF filesystem setup.
// The control plane is blocked on the handoff socket waiting for it: it opened
// the filesystem context itself (the superblock's owning user namespace is
// stamped by whoever calls fsopen, so nobody else can open it on its behalf)
// and needs a process with init-namespace CAP_SYS_ADMIN to set the delegation
// parameters and mount the pin filesystem.
//
// The helper is embedded rather than installed: it is written to a private
// temporary directory, run once under sudo, and removed. Nothing is left on
// the user's system, and nothing about clawker's install has to change for a
// rootless host to work.
func delegateBPFFS(ctx context.Context, sos *cpmanager.CPSOSError, ios *iostreams.IOStreams) error {
	if runtime.GOOS != "linux" {
		// The mount has to happen on the kernel the daemon runs on. A CLI
		// elsewhere (a Docker Desktop VM, say) cannot reach it — and cannot
		// be in this situation either, since that daemon runs as root.
		return sos
	}

	firewallDir, err := consts.FirewallDataSubdir()
	if err != nil {
		return fmt.Errorf("resolving the handoff socket directory: %w", err)
	}
	// Resolving the pin directory creates it. That matters: the helper
	// refuses to create its own mount point, so that a run as root cannot
	// leave a root-owned directory behind where the unprivileged control
	// plane expects to write.
	pinPath, err := consts.BPFFSSubdir()
	if err != nil {
		return fmt.Errorf("resolving the BPF filesystem directory: %w", err)
	}

	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("%w\n\nCompleting it needs sudo, which is not on PATH", sos)
	}

	helperPath, cleanup, err := stageDelegateHelper()
	if err != nil {
		return err
	}
	defer cleanup()

	// The container-start path runs this bootstrap under a spinner, which
	// renders to the same stream the prompt is about to land on. Stopping it
	// is a no-op when nothing is spinning, and the caller's own deferred stop
	// stays safe either way.
	ios.StopSpinner()

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), sos.Message)
	fmt.Fprintf(ios.ErrOut, "%s Completing it needs %s.\n", cs.InfoIcon(), cs.Muted("sudo"))

	credential, err := cmdutil.SudoPassword(ios)
	if err != nil {
		return fmt.Errorf("%w\n\n%w", sos, err)
	}

	// -S reads the credential from stdin and -p silences sudo's own prompt,
	// which was already asked above. The helper reads nothing from stdin, so
	// what it inherits is a reader sudo has already drained.
	cmd := exec.CommandContext(ctx, sudoPath, "-S", "-p", "", helperPath,
		filepath.Join(firewallDir, delegation.SocketName), pinPath)
	cmd.Stdin = strings.NewReader(credential + "\n")
	cmd.Stdout, cmd.Stderr = ios.Out, ios.ErrOut
	if runErr := cmd.Run(); runErr != nil {
		return fmt.Errorf("%w\n\nThe elevated helper failed: %w", sos, runErr)
	}
	return nil
}

// stageDelegateHelper writes the embedded helper to a fresh private directory
// and returns its path plus a cleanup.
//
// A fresh directory rather than a fixed path in the temp root: this binary is
// about to be executed as root, and a fixed path in a world-writable
// directory is a name another local user can win a race for. The directory is
// created 0700 by MkdirTemp and holds nothing else.
func stageDelegateHelper() (string, func(), error) {
	dir, err := os.MkdirTemp("", consts.NamePrefix+"-bpffs-")
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

	path := filepath.Join(dir, helperBinaryName)
	if werr := os.WriteFile(path, cpmanager.BPFFSDelegateBinary, helperFileMode); werr != nil {
		cleanup()
		return "", nil, fmt.Errorf("writing the elevated helper: %w", werr)
	}
	return path, cleanup, nil
}
