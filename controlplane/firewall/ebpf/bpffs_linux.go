//go:build linux

package ebpf

import (
	"fmt"

	"golang.org/x/sys/unix"
)

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
