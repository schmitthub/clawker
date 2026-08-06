//go:build linux

// Command idmap-mount is the elevated half of clawker's rootless workspace
// setup. Rootless Docker maps the daemon user to container root, so a
// bind-mounted workspace appears root-owned inside the container and the
// unprivileged clawker user cannot use it. The fix is an ID-mapped bind — a
// kernel-level view of the workspace that presents the owner's files as the
// IDs the container's clawker user occupies — and creating one is reserved
// for init-namespace CAP_SYS_ADMIN, which is why this program exists.
//
// It is deliberately not a clawker command. It reads no config, writes no
// state, opens no log, and touches nothing but the two paths it is handed —
// because it runs under sudo, and the smallest thing that can do the job is
// the only acceptable thing to run as root.
//
//	idmap-mount <source-dir> <view-dir> <uid-from:uid-to> <gid-from:gid-to>
//
// It builds a throwaway user namespace holding exactly the requested ID
// pairs, clones the source into a detached mount, stamps the namespace onto
// it (MOUNT_ATTR_IDMAP), attaches it at the view path, and proves the result
// by checking the view's presented owner before exiting. The namespace's
// last reference dies with the mount; nothing persists but the mount itself,
// which lives until unmounted or reboot.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/schmitthub/clawker/internal/idmap"
)

const (
	// usageArgs is argv without the program name: source, view, uid pair,
	// gid pair.
	usageArgs = 4

	// exitUsage is the conventional shell exit code for a usage error,
	// distinct from exit 1 so a caller can tell "invoked wrong" from "the
	// job failed".
	exitUsage = 2

	// childEnv marks the re-exec of this binary whose only job is to hold
	// the throwaway user namespace open while the parent stamps it onto the
	// cloned mount.
	childEnv = "CLAWKER_IDMAP_MOUNT_CHILD"

	// usage is the one-line invocation summary.
	usage = "usage: %s <source-dir> <view-dir> <uid-from:uid-to> <gid-from:gid-to>\n"
)

func main() {
	if os.Getenv(childEnv) != "" {
		holdOpen()
		return
	}

	args := os.Args[1:]
	if len(args) != usageArgs {
		fmt.Fprintf(os.Stderr, usage, filepath.Base(os.Args[0]))
		os.Exit(exitUsage)
	}

	if err := run(args[0], args[1], args[2], args[3]); err != nil {
		fmt.Fprintf(os.Stderr, "idmap-mount: %v\n", err)
		os.Exit(1)
	}
}

// holdOpen is the namespace holder's whole life: block until the parent
// closes our stdin, then exit so the namespace's only remaining reference is
// the mount the parent stamped.
func holdOpen() {
	buf := make([]byte, 1)
	// EOF and error both mean the parent is done with us; neither is
	// actionable and there is nothing left to do but return.
	if _, err := os.Stdin.Read(buf); err != nil {
		return
	}
}

func run(src, view, uidPair, gidPair string) error {
	m, err := parseArgs(src, view, uidPair, gidPair)
	if err != nil {
		return err
	}

	usernsFD, release, nsErr := holdUserNamespace(m)
	if nsErr != nil {
		return fmt.Errorf("building the ID-mapping namespace: %w", nsErr)
	}
	defer release()

	if attachErr := attachView(src, view, usernsFD); attachErr != nil {
		return attachErr
	}
	if proveErr := proveMapping(view, m); proveErr != nil {
		return proveErr
	}

	// Success is silent, like mount(8): the caller already narrates what it
	// is doing, and errors carry their own detail.
	return nil
}

// parseArgs validates the privilege level, the two paths, and the two ID
// pairs before anything touches the kernel.
func parseArgs(src, view, uidPair, gidPair string) (idmap.Mapping, error) {
	if os.Geteuid() != 0 {
		return idmap.Mapping{}, fmt.Errorf(
			"must run as root (running uid %d): creating an ID-mapped mount needs init-namespace CAP_SYS_ADMIN",
			os.Geteuid())
	}
	uidFrom, uidTo, err := idmap.ParseIDPair(uidPair)
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("uid pair: %w", err)
	}
	gidFrom, gidTo, err := idmap.ParseIDPair(gidPair)
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("gid pair: %w", err)
	}
	if srcErr := checkDir(src); srcErr != nil {
		return idmap.Mapping{}, fmt.Errorf("source: %w", srcErr)
	}
	// The view directory must already exist: this program refuses to create
	// it, so a run as root can never leave a root-owned directory where the
	// unprivileged CLI expects to manage state.
	if viewErr := checkDir(view); viewErr != nil {
		return idmap.Mapping{}, fmt.Errorf("view: %w", viewErr)
	}
	return idmap.Mapping{FromUID: uidFrom, ToUID: uidTo, FromGID: gidFrom, ToGID: gidTo}, nil
}

// attachView clones the source tree into a detached mount, stamps the ID
// mapping onto the clone, and moves it into place at the view path,
// replacing whatever was mounted there before.
func attachView(src, view string, usernsFD int) error {
	treeFD, err := unix.OpenTree(unix.AT_FDCWD, src, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}
	defer closeFD(treeFD)

	//nolint:exhaustruct // the other MountAttr fields are the mount flags this call deliberately leaves alone
	attr := unix.MountAttr{
		Attr_set:  unix.MOUNT_ATTR_IDMAP,
		Userns_fd: uint64(usernsFD), //nolint:gosec // a file descriptor is never negative
	}
	if setErr := unix.MountSetattr(treeFD, "", unix.AT_EMPTY_PATH, &attr); setErr != nil {
		return fmt.Errorf("stamping the ID mapping (does the filesystem support ID-mapped mounts?): %w", setErr)
	}

	// A previous view at this path holds a stale mapping (or predates a
	// changed one); detach it so the fresh attach below is what the daemon
	// re-binds at the next container start.
	if idmap.Mounted(view) {
		if umountErr := unix.Unmount(view, unix.MNT_DETACH); umountErr != nil {
			return fmt.Errorf("detaching the previous view at %s: %w", view, umountErr)
		}
	}

	if mvErr := unix.MoveMount(treeFD, "", unix.AT_FDCWD, view, unix.MOVE_MOUNT_F_EMPTY_PATH); mvErr != nil {
		return fmt.Errorf("attaching the view at %s: %w", view, mvErr)
	}
	return nil
}

// proveMapping refuses to report success on a view that does not actually
// translate. The view's root is the source directory, owned on disk by the
// from-IDs, so through the view it must present the to-IDs. A wrong
// direction or an ignored mapping fails loud here instead of surfacing later
// as container EACCES.
func proveMapping(view string, m idmap.Mapping) error {
	var st unix.Stat_t
	if err := unix.Stat(view, &st); err != nil {
		return fmt.Errorf("verifying the view: %w", err)
	}
	if st.Uid != m.ToUID || st.Gid != m.ToGID {
		return fmt.Errorf("view presents %d:%d, expected %d:%d — the mapping did not take",
			st.Uid, st.Gid, m.ToUID, m.ToGID)
	}
	return nil
}

// checkDir requires an existing absolute directory path.
func checkDir(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s is not an absolute path", path)
	}
	//nolint:gosec // the paths are this program's whole argument; they are chosen by the caller by design
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

// holdUserNamespace spawns a child of this binary in a fresh user namespace,
// writes the requested ID pairs as its maps, and returns an open descriptor
// to the namespace plus a release func for the child. The namespace object
// itself outlives the child: once MOUNT_ATTR_IDMAP stamps it onto the mount,
// the mount holds the reference.
func holdUserNamespace(m idmap.Mapping) (int, func(), error) {
	// The holder is this same binary with no arguments and one env marker;
	// there is no cancellation story to model, so the background context is
	// the honest one.
	child := exec.CommandContext(context.Background(), "/proc/self/exe")
	child.Env = append(os.Environ(), childEnv+"=1")
	//nolint:exhaustruct // every other SysProcAttr field is a namespace/credential knob this deliberately leaves at its zero value
	child.SysProcAttr = &unix.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER}

	stdin, err := child.StdinPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("preparing the namespace holder: %w", err)
	}
	if startErr := child.Start(); startErr != nil {
		return 0, nil, fmt.Errorf("starting the namespace holder: %w", startErr)
	}
	release := func() {
		// Closing stdin is the shutdown signal and Wait reaps. Both are
		// best-effort: the holder's exit status carries no information the
		// caller could act on.
		if closeErr := stdin.Close(); closeErr != nil {
			_ = closeErr
		}
		if waitErr := child.Wait(); waitErr != nil {
			_ = waitErr
		}
	}

	if mapErr := writeIDMaps(child.Process.Pid, m); mapErr != nil {
		release()
		return 0, nil, mapErr
	}

	usernsFD, openErr := unix.Open(fmt.Sprintf("/proc/%d/ns/user", child.Process.Pid),
		unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if openErr != nil {
		release()
		return 0, nil, fmt.Errorf("opening the namespace: %w", openErr)
	}

	releaseAll := func() {
		// The mount holds its own namespace reference after MountSetattr;
		// this descriptor and the holder child are both disposable.
		closeFD(usernsFD)
		release()
	}
	return usernsFD, releaseAll, nil
}

// writeIDMaps installs the single-row uid and gid maps on the holder. Rows
// are "inside outside count": the inside ID is the on-disk ID the mount
// translates FROM, the outside ID is the kernel ID it presents. Root writing
// a single-line map into a child namespace needs no setgroups dance.
func writeIDMaps(pid int, m idmap.Mapping) error {
	maps := []struct {
		file string
		row  string
	}{
		{file: "uid_map", row: fmt.Sprintf("%d %d 1\n", m.FromUID, m.ToUID)},
		{file: "gid_map", row: fmt.Sprintf("%d %d 1\n", m.FromGID, m.ToGID)},
	}
	for _, entry := range maps {
		path := fmt.Sprintf("/proc/%d/%s", pid, entry.file)
		if err := os.WriteFile(path, []byte(entry.row), 0); err != nil {
			return fmt.Errorf("writing %s: %w", entry.file, err)
		}
	}
	return nil
}

// closeFD drops a descriptor whose close error is not actionable — the work
// it guarded has already succeeded or failed on its own terms.
func closeFD(fd int) {
	if err := unix.Close(fd); err != nil {
		_ = err
	}
}
