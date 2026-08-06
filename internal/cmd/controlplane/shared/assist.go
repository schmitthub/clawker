// Package shared holds the helpers the control plane bootstrap verbs have in
// common — currently the assistance a blocked control plane boot asks for.
package shared

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// helperBinaryName is what the staged helper is called on disk. It shows up
// in the sudo prompt and in the audit log, so it says what it does.
const helperBinaryName = consts.NamePrefix + "-bpffs-delegate"

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

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), sos.Message)
	fmt.Fprintf(ios.ErrOut, "%s Completing it needs %s.\n", cs.InfoIcon(), cs.Muted("sudo"))

	if runErr := cmdutil.RunElevated(ctx, ios, cmdutil.ElevatedHelper{
		Name:   helperBinaryName,
		Binary: cpmanager.BPFFSDelegateBinary,
		Args:   []string{filepath.Join(firewallDir, delegation.SocketName), pinPath},
	}); runErr != nil {
		return fmt.Errorf("%w\n\n%w", sos, runErr)
	}
	return nil
}
