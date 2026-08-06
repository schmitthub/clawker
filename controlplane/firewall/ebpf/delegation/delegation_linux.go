//go:build linux

package delegation

import "golang.org/x/sys/unix"

// bpfFSMagic identifies a mounted BPF filesystem in statfs results.
const bpfFSMagic = 0xcafe4a11

// Mounted reports whether path carries a BPF filesystem. It is the state
// check both sides of the privilege boundary share: the CLI-side container
// spec builders use it to decide whether a delegated bpffs exists to bind,
// and the elevated helper uses it to recognise a stale mount it must replace.
func Mounted(path string) bool {
	var sfs unix.Statfs_t
	if err := unix.Statfs(path, &sfs); err != nil {
		return false
	}
	return sfs.Type == bpfFSMagic
}
