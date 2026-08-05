//go:build linux

package ebpf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
)

// bpfTokenCreate is the BPF_TOKEN_CREATE command. cilium/ebpf mints tokens
// itself, but it does so lazily and swallows the outcome, so the one below
// is raised directly: a mount that cannot mint a token is useless, and
// discovering that at first map creation would surface as an unrelated
// permission error much later.
const bpfTokenCreate = 36

// tokenCreateAttr is the BPF_TOKEN_CREATE argument. The bpf syscall accepts
// a short attribute as long as the caller declares its size.
type tokenCreateAttr struct {
	Flags   uint32
	BpffsFd uint32
}

// TokenFS is the BPF filesystem clawker mints tokens from. It exists only
// to carry delegated privileges: nothing is ever pinned here, and it stays
// private to this mount namespace.
//
// Its lifecycle has two shapes, and which one applies is discovered rather
// than detected:
//
//   - Init-namespace root (the rootful deployment) configures and
//     instantiates it in-process. No helper, no socket, no assistance.
//   - Inside a user namespace (the rootless deployment) the kernel refuses
//     the delegation options, because they demand init-namespace
//     CAP_SYS_ADMIN. The filesystem context stays open and an elevated
//     helper completes it — a file descriptor cannot travel over gRPC, so
//     it travels over a unix socket the way descriptors always have.
type TokenFS struct {
	fsFD       int
	mountPoint string
	delegated  bool
}

// OpenTokenFS opens a BPF filesystem context and tries to instantiate it
// with clawker's delegation masks.
//
// A nil error means the filesystem is ready to Mount. ErrDelegationRequired
// means the context is open and held, and Delegate must run first — that is
// the recoverable case the control plane publishes an SOS for. Any other
// error is terminal, and no descriptor is left open.
//
// The superblock's owning user namespace is stamped from THIS call, in this
// process. That is why the sequence cannot simply be handed to the helper
// wholesale: a filesystem the helper opened would be owned by the init
// namespace, and a token can only be minted from one owned by the minter's
// own namespace.
func OpenTokenFS(mountPoint string) (*TokenFS, error) {
	fsFD, err := unix.Fsopen("bpf", unix.FSOPEN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("ebpf: opening a BPF filesystem context: %w", err)
	}

	t := &TokenFS{fsFD: fsFD, mountPoint: mountPoint, delegated: false}

	switch cfgErr := t.configure(); {
	case cfgErr == nil:
		return t, nil
	case errors.Is(cfgErr, unix.EPERM), errors.Is(cfgErr, unix.EACCES):
		// Held open deliberately: the descriptor IS the thing the helper
		// needs, so it must outlive this call.
		t.delegated = true
		return t, fmt.Errorf("%w: %w", ErrDelegationRequired, cfgErr)
	default:
		t.Close()
		return nil, cfgErr
	}
}

// configure applies the delegation masks and instantiates the superblock.
// Both halves need init-namespace CAP_SYS_ADMIN, so inside a user namespace
// this is where the sequence stops.
func (t *TokenFS) configure() error {
	for _, p := range delegation.Params() {
		if err := unix.FsconfigSetString(t.fsFD, p.Name, p.Value); err != nil {
			return fmt.Errorf("ebpf: setting %s=%s: %w", p.Name, p.Value, err)
		}
	}
	if err := unix.FsconfigCreate(t.fsFD); err != nil {
		return fmt.Errorf("ebpf: instantiating the BPF filesystem: %w", err)
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
func (t *TokenFS) Delegate(ctx context.Context, socketPath string) error {
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
func (t *TokenFS) exchange(conn *net.UnixConn) error {
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

// Mount attaches the instantiated filesystem at its mount point and proves
// it can mint a token.
//
// The proof is not ceremony. cilium/ebpf discovers BPF filesystems by
// filesystem type and mints its token lazily, caching the result — including
// a nil one — for the lifetime of the process. A mount that looks fine but
// cannot mint would therefore surface much later as an unexplained
// permission failure on the first map creation, with nothing pointing back
// here. Failing loudly now is the difference between a diagnosable error and
// a mystery.
func (t *TokenFS) Mount() error {
	mntFD, err := unix.Fsmount(t.fsFD, unix.FSMOUNT_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("ebpf: creating a mount from the BPF filesystem: %w", err)
	}
	defer func() { _ = unix.Close(mntFD) }()

	if mkErr := os.MkdirAll(t.mountPoint, 0o700); mkErr != nil {
		return fmt.Errorf("ebpf: creating the token mount point %s: %w", t.mountPoint, mkErr)
	}
	if mvErr := unix.MoveMount(mntFD, "", unix.AT_FDCWD, t.mountPoint,
		unix.MOVE_MOUNT_F_EMPTY_PATH); mvErr != nil {
		return fmt.Errorf("ebpf: attaching the BPF filesystem at %s: %w", t.mountPoint, mvErr)
	}

	return proveToken(t.mountPoint)
}

// Delegated reports whether the kernel refused this process the delegation
// options, meaning an elevated helper completed the filesystem.
func (t *TokenFS) Delegated() bool { return t.delegated }

// Close releases the filesystem context descriptor. It is safe to call more
// than once, and after Mount — the mount holds its own reference.
func (t *TokenFS) Close() {
	if t.fsFD >= 0 {
		_ = unix.Close(t.fsFD)
		t.fsFD = -1
	}
}

// proveToken mints a token directly from the mount, confirming the
// delegation actually landed.
func proveToken(mountPoint string) error {
	dirFD, err := unix.Open(mountPoint, unix.O_DIRECTORY|unix.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("ebpf: opening the token mount %s: %w", mountPoint, err)
	}
	defer func() { _ = unix.Close(dirFD) }()

	attr := tokenCreateAttr{Flags: 0, BpffsFd: uint32(dirFD)} //nolint:gosec // a file descriptor is never negative here

	// The unsafe use is the bpf(2) kernel ABI — a pointer to a fixed-size,
	// stack-local attr union plus its size. No attacker-controlled memory.
	// The uintptr conversion stays inside the Syscall argument expression so
	// the pointer remains pinned for the call.
	// nosemgrep: go.lang.security.audit.unsafe.use-of-unsafe-block -- bpf(2) kernel ABI, see above
	attrPtr := unsafe.Pointer(&attr)
	// nosemgrep: go.lang.security.audit.unsafe.use-of-unsafe-block -- bpf(2) kernel ABI, see above
	attrSize := unsafe.Sizeof(attr)
	tokenFD, _, errno := unix.Syscall(unix.SYS_BPF, bpfTokenCreate, uintptr(attrPtr), attrSize)
	if errno != 0 {
		return fmt.Errorf("ebpf: the BPF filesystem at %s cannot mint tokens: %w", mountPoint, errno)
	}
	_ = unix.Close(int(tokenFD))
	return nil
}
