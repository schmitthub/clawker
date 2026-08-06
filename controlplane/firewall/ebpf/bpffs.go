package ebpf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// BPF filesystem delegation is the rootless arm of the control plane's eBPF
// setup, and it only ever runs after the default path has failed: the CP
// loads against whatever BPF filesystem its container spec bound at
// consts.SysFSBPFPath (the host's own /sys/fs/bpf by default), and only a
// permission-denied load — the rootless Docker shape, where that filesystem
// belongs to the init namespace and this process does not — triggers any of
// the machinery in this file.
//
// The delegated filesystem serves both jobs at once: its superblock is
// stamped with this process's user namespace (fsopen here, see
// OpenForDelegation), so BPF tokens can be minted from it, and the elevated
// helper attaches it at clawker's own host path owned by the control plane
// through uid/gid/mode filesystem parameters — never a chown, and never a
// permission change on a path clawker does not own. A fresh CP container
// then binds that path and loads normally; cilium/ebpf discovers the
// filesystem by type and mints its token with no code on our side.
//
// The delegation masks the helper applies live in the delegation subpackage,
// not here: a binary that runs as root should link the syscalls it makes and
// nothing else, least of all this loader.

// Minimum kernel for the delegation path. 6.9 is where the BPF token
// landed for good: 6.8 carries the uid/gid/mode bpffs mount options but had
// the whole token feature reverted before release (kernel commits
// 750e785796bb, d17aff807f84, then b08c8fc0411d re-adding uid/gid), so
// kernel/bpf/inode.c gains its delegate_cmds/maps/progs/attachs mount
// parameters only in 6.9.
//
// The version is checked BEFORE any of the sequence is attempted. What an
// older kernel does when handed a delegation option it does not know is not
// something to discover in a user's environment.
const (
	MinKernelMajor = 6
	MinKernelMinor = 9
)

// ErrKernelUnsupported reports a kernel too old for BPF filesystem
// delegation. It is terminal: no amount of assistance makes an older kernel
// grow the feature, so the control plane fails startup with it rather than
// asking the CLI for help it cannot give.
var ErrKernelUnsupported = errors.New("ebpf: kernel too old for BPF filesystem delegation")

// ErrDelegationRequired reports that the kernel refused an operation this
// process is not privileged to perform, and that an elevated helper must
// perform it instead. It is the one recoverable outcome of the BPF
// filesystem setup — everything else is terminal.
var ErrDelegationRequired = errors.New("ebpf: BPF filesystem setup requires elevated assistance")

// ErrUnsupportedPlatform reports BPF filesystem setup attempted somewhere
// with no BPF filesystem. The control plane only ever runs on Linux; this
// exists because the package is compiled into the CLI, which does not.
var ErrUnsupportedPlatform = errors.New("ebpf: BPF filesystem is Linux-only")

// minReleaseParts is the major and minor a release string must carry; a
// patch level and a distribution flavour may follow and are ignored.
const minReleaseParts = 2

// kernelVersion is a parsed major.minor from a uname release string.
type kernelVersion struct {
	major int
	minor int
}

// atLeast reports whether v is at least major.minor.
func (v kernelVersion) atLeast(major, minor int) bool {
	if v.major != major {
		return v.major > major
	}
	return v.minor >= minor
}

func (v kernelVersion) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor)
}

// parseKernelRelease extracts major.minor from a uname release string such
// as "6.11.0-28-generic" or "6.9.0". Everything after the minor number is
// ignored: distributions append their own patch levels, build numbers and
// flavour suffixes, and none of them change whether the feature exists.
func parseKernelRelease(release string) (kernelVersion, error) {
	if release == "" {
		return kernelVersion{}, errors.New("empty kernel release")
	}

	// Trim the flavour suffix first ("-28-generic"), then split on dots, so
	// a release with no patch component ("6.9-rc1") parses the same way.
	base, _, _ := strings.Cut(release, "-")
	parts := strings.Split(base, ".")
	if len(parts) < minReleaseParts {
		return kernelVersion{}, fmt.Errorf("malformed kernel release %q", release)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return kernelVersion{}, fmt.Errorf("malformed major version in kernel release %q: %w", release, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return kernelVersion{}, fmt.Errorf("malformed minor version in kernel release %q: %w", release, err)
	}
	return kernelVersion{major: major, minor: minor}, nil
}
