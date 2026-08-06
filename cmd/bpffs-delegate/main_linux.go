//go:build linux

// Command bpffs-delegate is the elevated half of clawker's BPF filesystem
// setup. On a rootless Docker host the control plane runs inside a user
// namespace, where the kernel reserves two operations for init-namespace
// CAP_SYS_ADMIN: setting a BPF filesystem's delegation parameters, and
// mounting a BPF filesystem at all. This program performs exactly those two
// operations and exits.
//
// It is deliberately not a clawker command. It reads no config, writes no
// state, opens no log, and never speaks to the control plane over anything
// but the one socket it is handed — because it runs under sudo, and the
// smallest thing that can do the job is the only acceptable thing to run as
// root. Its arguments are two paths and its output is one byte.
//
//	bpffs-delegate <handoff-socket> <pin-path>
//
// The control plane opens the filesystem context itself and passes the
// descriptor over the socket, which is not an implementation detail: the
// superblock's owning user namespace is stamped by whoever calls fsopen, and
// a token can only be minted from a filesystem owned by the minter's own
// namespace. A filesystem this program opened would be useless to it.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
)

const (
	// usageArgs is argv without the program name: socket, then pin path.
	usageArgs = 2

	// exitUsage is the conventional shell exit code for a usage error, kept
	// distinct from exit 1 so a caller can tell "invoked wrong" from "the
	// job failed".
	exitUsage = 2

	// passedFDs is how many descriptors the control plane sends: the one
	// filesystem context, and nothing else. fdSize is what one costs in the
	// ancillary buffer.
	passedFDs = 1
	fdSize    = 4

	// connectTimeout bounds the wait for the control plane's listener. The
	// socket is created moments after the SOS is published, so this covers
	// the ordering race rather than a long absence — a control plane that
	// never listens is a failure, not something to sit on.
	connectTimeout  = 15 * time.Second
	connectInterval = 100 * time.Millisecond

	// exchangeTimeout bounds the descriptor read once connected.
	exchangeTimeout = 10 * time.Second

	// ackFailed is any byte that is not delegation.AckOK. Writing it back
	// turns a failure here into an immediate, explicit failure on the
	// control plane side instead of a socket that closes without a word.
	ackFailed = 'x'
)

func main() {
	if len(os.Args)-1 != usageArgs {
		fmt.Fprintf(os.Stderr, "usage: %s <handoff-socket> <pin-path>\n", os.Args[0])
		os.Exit(exitUsage)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintf(os.Stderr, "bpffs-delegate: %v\n", err)
		os.Exit(1)
	}
}

// run connects to the control plane, takes the filesystem context it offers,
// and completes both privileged jobs before acknowledging. The acknowledgement
// is sent last on purpose: it means every job succeeded, so the control plane
// never proceeds on a half-finished filesystem.
func run(socketPath, pinPath string) error {
	conn, err := connect(socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	fsFD, err := receiveFD(conn)
	if err != nil {
		reportFailure(conn)
		return err
	}
	defer func() { _ = unix.Close(fsFD) }()

	uid, gid, err := peerCreds(conn)
	if err != nil {
		reportFailure(conn)
		return err
	}

	if cfgErr := configure(fsFD, uid, gid); cfgErr != nil {
		reportFailure(conn)
		return cfgErr
	}
	if mountErr := mountAt(pinPath, fsFD); mountErr != nil {
		reportFailure(conn)
		return mountErr
	}

	if _, ackErr := conn.Write([]byte{delegation.AckOK}); ackErr != nil {
		return fmt.Errorf("acknowledging: %w", ackErr)
	}
	return nil
}

// connect retries until the control plane's listener exists. The SOS is
// published just before the socket is created, so a helper started promptly
// can legitimately arrive first.
func connect(socketPath string) (*net.UnixConn, error) {
	addr := &net.UnixAddr{Name: socketPath, Net: "unix"}
	deadline := time.Now().Add(connectTimeout)
	for {
		conn, err := net.DialUnix("unix", nil, addr)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connecting to %s: %w", socketPath, err)
		}
		time.Sleep(connectInterval)
	}
}

// receiveFD reads the BPF filesystem context descriptor off the socket. The
// control plane sends it with no payload of its own; the kernel requires at
// least one data byte alongside ancillary data on a stream socket, so one
// arrives and is discarded.
func receiveFD(conn *net.UnixConn) (int, error) {
	if err := conn.SetReadDeadline(time.Now().Add(exchangeTimeout)); err != nil {
		return -1, fmt.Errorf("setting the read deadline: %w", err)
	}

	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(passedFDs*fdSize))
	//nolint:dogsled // ReadMsgUnix reports five values; only the ancillary length and the error matter here
	_, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return -1, fmt.Errorf("receiving the BPF filesystem context: %w", err)
	}

	messages, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return -1, fmt.Errorf("parsing the control message: %w", err)
	}
	if len(messages) != passedFDs {
		return -1, fmt.Errorf("expected one control message, got %d", len(messages))
	}
	fds, err := unix.ParseUnixRights(&messages[0])
	if err != nil {
		return -1, fmt.Errorf("parsing the passed descriptor: %w", err)
	}
	if len(fds) != passedFDs {
		return -1, fmt.Errorf("expected one descriptor, got %d", len(fds))
	}
	return fds[0], nil
}

// configure applies clawker's delegation parameters plus the ownership
// parameters and instantiates the superblock. Both halves need
// init-namespace CAP_SYS_ADMIN, which is the entire reason this program
// exists. Ownership comes from the peer's credentials, so the filesystem is
// born owned by the control plane — never a chown afterwards.
func configure(fsFD, uid, gid int) error {
	params := append(delegation.Params(), delegation.OwnerParams(uid, gid)...)
	for _, p := range params {
		if err := unix.FsconfigSetString(fsFD, p.Name, p.Value); err != nil {
			return fmt.Errorf("setting %s=%s: %w", p.Name, p.Value, err)
		}
	}
	if err := unix.FsconfigCreate(fsFD); err != nil {
		return fmt.Errorf("instantiating the BPF filesystem: %w", err)
	}
	return nil
}

// peerCreds reads the connected control plane's own uid and gid. Ownership of
// the pin filesystem is LEARNED from the peer, never configured or passed in:
// the process that has to write pins is the process on the other end of this
// socket, and its credentials are translated into this namespace by the
// kernel rather than asserted by anyone.
func peerCreds(conn *net.UnixConn) (int, int, error) {
	raw, rawErr := conn.SyscallConn()
	if rawErr != nil {
		return 0, 0, fmt.Errorf("accessing the connection: %w", rawErr)
	}

	var ucred *unix.Ucred
	var credErr error
	if ctrlErr := raw.Control(func(fd uintptr) {
		ucred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); ctrlErr != nil {
		return 0, 0, fmt.Errorf("accessing the connection: %w", ctrlErr)
	}
	if credErr != nil {
		return 0, 0, fmt.Errorf("reading peer credentials: %w", credErr)
	}
	return int(ucred.Uid), int(ucred.Gid), nil
}

// mountAt attaches the delegated filesystem at path. Nothing is created
// here: the mount point is the clawker data directory the CLI resolved
// before invoking this, and a root-owned directory left behind at that path
// would outlive the mount.
//
// An existing BPF filesystem at path is REPLACED, not adopted. This program
// only ever runs because the control plane's load failed on permissions, and
// a mount already sitting there in that situation is a stale one whose
// owning user namespace died with a previous Docker daemon — adopting it
// would hand the control plane a filesystem that can never mint a token
// again.
func mountAt(path string, fsFD int) error {
	//nolint:gosec // the mount point is this program's whole argument; it is chosen by the caller by design
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the mount point %s must exist: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the mount point %s is not a directory", path)
	}

	if delegation.Mounted(path) {
		if umountErr := unix.Unmount(path, unix.MNT_DETACH); umountErr != nil {
			return fmt.Errorf("detaching the stale BPF filesystem at %s: %w", path, umountErr)
		}
	}

	mntFD, err := unix.Fsmount(fsFD, unix.FSMOUNT_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("creating a mount from the BPF filesystem: %w", err)
	}
	defer func() { _ = unix.Close(mntFD) }()

	if mvErr := unix.MoveMount(mntFD, "", unix.AT_FDCWD, path,
		unix.MOVE_MOUNT_F_EMPTY_PATH); mvErr != nil {
		return fmt.Errorf("attaching the BPF filesystem at %s: %w", path, mvErr)
	}
	return nil
}

// reportFailure tells the control plane this failed, so it stops waiting now
// and reports a delegation failure rather than a timeout. Best effort: the
// real error is already on its way to the caller.
func reportFailure(conn *net.UnixConn) {
	if _, err := conn.Write([]byte{ackFailed}); err != nil && !errors.Is(err, net.ErrClosed) {
		fmt.Fprintf(os.Stderr, "bpffs-delegate: reporting failure: %v\n", err)
	}
}
