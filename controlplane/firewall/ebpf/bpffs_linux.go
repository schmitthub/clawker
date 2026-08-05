//go:build linux

package ebpf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
)

// bpfFSMagic identifies a mounted BPF filesystem, so a path is checked for
// the filesystem itself rather than for a directory that merely exists.
const bpfFSMagic = 0xcafe4a11

// pinFSPollInterval paces the wait for a delegated mount to appear. The
// mount propagates the instant the helper creates it; this only bounds how
// quickly that is noticed.
const pinFSPollInterval = 200 * time.Millisecond

// CheckKernelSupport reports whether the running kernel can do BPF
// filesystem delegation at all. Callers run it BEFORE attempting any of the
// sequence: an older kernel's behaviour when handed delegation options it
// does not recognise is not something to characterise in a user's
// environment, so the version is the gate and the attempt never happens.
func CheckKernelSupport() error {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return fmt.Errorf("ebpf: reading kernel version: %w", err)
	}

	release := unix.ByteSliceToString(uts.Release[:])
	version, err := parseKernelRelease(release)
	if err != nil {
		return fmt.Errorf("ebpf: %w", err)
	}
	if !version.atLeast(MinKernelMajor, MinKernelMinor) {
		return fmt.Errorf("%w: running %s (%s), need %d.%d or newer",
			ErrKernelUnsupported, version, release, MinKernelMajor, MinKernelMinor)
	}
	return nil
}

// PinFSMounted reports whether a BPF filesystem is mounted at path.
func PinFSMounted(path string) bool {
	var sfs unix.Statfs_t
	if err := unix.Statfs(path, &sfs); err != nil {
		return false
	}
	return sfs.Type == bpfFSMagic
}

// MountPinFS mounts the pin filesystem at path, owned by uid/gid through
// mount options so no chown is needed afterwards. It is idempotent: an
// existing BPF filesystem at path is left alone.
//
// This succeeds when the caller is init-namespace root — the rootful
// deployment, where no delegation and no token are involved at all. When
// the kernel refuses it, the caller is in a user namespace and the mount
// must be performed by an elevated helper instead: that case returns
// ErrDelegationRequired, which is the recoverable outcome the control plane
// asks the CLI for help with. Every other failure is terminal.
func MountPinFS(path string, uid, gid int) error {
	if PinFSMounted(path) {
		return nil
	}
	// The mode here barely matters: the bpffs mounted over this directory
	// brings its own, from the mount options below.
	if err := os.MkdirAll(path, 0o750); err != nil {
		return fmt.Errorf("ebpf: creating BPF filesystem mount point %s: %w", path, err)
	}

	// Ownership and mode come from mount options rather than a chown: the
	// kernel added uid/gid for exactly this, so the filesystem is born
	// owned by the right user instead of being adjusted afterwards. The
	// elevated helper mounts it with the same options on the rootless path.
	opts := delegation.MountOptions(uid, gid)
	if err := unix.Mount("bpffs", path, delegation.FSType, 0, opts); err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return fmt.Errorf("%w: mounting a BPF filesystem at %s: %w",
				ErrDelegationRequired, path, err)
		}
		return fmt.Errorf("ebpf: mounting a BPF filesystem at %s with %q: %w", path, opts, err)
	}

	// Shared propagation is what lets the mount cross into the containers
	// that bind-mount this path — without it the filesystem stays private
	// to this mount namespace and the CoreDNS container never sees the
	// pinned dns_cache map.
	if err := unix.Mount("", path, "", unix.MS_SHARED, ""); err != nil {
		return fmt.Errorf("ebpf: marking %s shared: %w", path, err)
	}
	return nil
}

// AwaitPinFS waits for the pin filesystem to appear at path, which is what
// the control plane does after asking for elevated assistance: the helper
// mounts it on the host and shared propagation carries it into this
// already-running container. Nothing is mounted here.
//
// It returns as soon as the filesystem is present, or when ctx is done.
func AwaitPinFS(ctx context.Context, path string) error {
	ticker := time.NewTicker(pinFSPollInterval)
	defer ticker.Stop()

	for {
		if PinFSMounted(path) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ebpf: waiting for a BPF filesystem at %s: %w", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
