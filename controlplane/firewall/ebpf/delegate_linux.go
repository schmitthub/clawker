//go:build linux

package ebpf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
)

// DelegatedFS is an open BPF filesystem context awaiting delegation. It is
// only ever created on the rootless recovery path: the control plane's load
// has already failed on permissions, and the filesystem this context becomes
// — configured by the elevated helper, attached by it at clawker's host
// path, and bound into a fresh CP container — is what the retry loads
// against.
//
// The context is opened HERE, in this process, and that is not an
// implementation detail: the superblock's owning user namespace is stamped
// from the fsopen caller, and a BPF token can only be minted from a
// filesystem owned by the minter's own namespace. A filesystem the helper
// opened would be owned by the init namespace and useless to any container.
type DelegatedFS struct {
	fsFD int
}

// OpenForDelegation opens a BPF filesystem context for the elevated helper
// to complete.
//
// It first tries to apply the delegation parameters itself, expecting the
// kernel to refuse — this only runs after a permission-denied load, which
// means the process is inside a user namespace and the parameters demand
// init-namespace CAP_SYS_ADMIN. That refusal is the good outcome: the
// context is returned open, ready to hand to the helper. If the kernel
// ACCEPTS the parameters, the process had the privileges all along and the
// load failure has some other cause delegation cannot fix; that is an error,
// and no descriptor is left open.
func OpenForDelegation() (*DelegatedFS, error) {
	fsFD, err := unix.Fsopen(delegation.FSType, unix.FSOPEN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("ebpf: opening a BPF filesystem context: %w", err)
	}

	t := &DelegatedFS{fsFD: fsFD}
	switch cfgErr := t.tryConfigure(); {
	case errors.Is(cfgErr, unix.EPERM), errors.Is(cfgErr, unix.EACCES):
		// Held open deliberately: the descriptor IS the thing the helper
		// needs, so it must outlive this call.
		return t, nil
	case cfgErr == nil:
		t.Close()
		return nil, errors.New(
			"ebpf: the kernel accepted the delegation parameters, so the load failure is not a delegation problem")
	default:
		t.Close()
		return nil, cfgErr
	}
}

// tryConfigure applies the delegation masks, probing whether the kernel
// would let this process do the helper's job. Inside a user namespace it
// refuses with EPERM, which is exactly the signal OpenForDelegation wants;
// the context stays configurable by the helper either way, because a
// refused fsconfig commits nothing.
func (t *DelegatedFS) tryConfigure() error {
	for _, p := range delegation.Params() {
		if err := unix.FsconfigSetString(t.fsFD, p.Name, p.Value); err != nil {
			return fmt.Errorf("ebpf: setting %s=%s: %w", p.Name, p.Value, err)
		}
	}
	return nil
}

// Delegate hands the open filesystem context to an elevated helper and
// waits for it to report back. It listens on socketPath, which must sit
// somewhere the helper can reach — a bind-mounted directory, since the
// helper runs on the host and this process does not.
//
// The socket is created 0600. The helper runs as root and is not subject to
// those permissions; nobody else has any business connecting.
func (t *DelegatedFS) Delegate(ctx context.Context, socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ebpf: clearing a stale handoff socket %s: %w", socketPath, err)
	}

	listener, err := listenForHelper(socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		if rerr := os.Remove(socketPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			// Nothing actionable: the listener is closed and the process is
			// either proceeding or exiting.
			_ = rerr
		}
	}()

	conn, err := acceptWithContext(ctx, listener)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	return t.exchange(conn)
}

// Close releases the filesystem context descriptor. It is safe to call more
// than once.
func (t *DelegatedFS) Close() {
	if t.fsFD >= 0 {
		_ = unix.Close(t.fsFD)
		t.fsFD = -1
	}
}

// acceptWithContext waits for the helper to connect, giving up when ctx is
// done. The accept runs on its own goroutine and reports through a buffered
// channel, so it can never block on a send after this function has returned;
// the caller's deferred Close is what unblocks the pending Accept.
func acceptWithContext(ctx context.Context, listener *net.UnixListener) (*net.UnixConn, error) {
	type accepted struct {
		conn *net.UnixConn
		err  error
	}

	results := make(chan accepted, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		results <- accepted{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("ebpf: waiting for the delegation helper: %w", ctx.Err())
	case result := <-results:
		if result.err != nil {
			return nil, fmt.Errorf("ebpf: waiting for the delegation helper: %w", result.err)
		}
		return result.conn, nil
	}
}

// listenForHelper opens the handoff socket, secured so only root — which the
// helper is — can reach it.
func listenForHelper(socketPath string) (*net.UnixListener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("ebpf: listening for the delegation helper on %s: %w", socketPath, err)
	}

	if chmodErr := os.Chmod(socketPath, 0o600); chmodErr != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("ebpf: securing the handoff socket %s: %w", socketPath, chmodErr)
	}
	return listener, nil
}

// exchange sends the filesystem context descriptor and waits for the
// helper's verdict.
func (t *DelegatedFS) exchange(conn *net.UnixConn) error {
	if _, _, err := conn.WriteMsgUnix(nil, unix.UnixRights(t.fsFD), nil); err != nil {
		return fmt.Errorf("ebpf: sending the BPF filesystem context: %w", err)
	}

	ack := make([]byte, 1)
	if _, err := conn.Read(ack); err != nil {
		return fmt.Errorf("ebpf: waiting for the delegation helper's result: %w", err)
	}
	if ack[0] != delegation.AckOK {
		return errors.New("ebpf: the delegation helper could not configure the BPF filesystem")
	}
	return nil
}
