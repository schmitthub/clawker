package ebpf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// The BPF filesystem clawker pins into is its own, not the system-wide
// /sys/fs/bpf. Two things live here:
//
//   - The PIN filesystem, mounted at PinPath. It holds clawker's maps and
//     programs and is shared with the CoreDNS container, which opens
//     dns_cache by absolute path. It is an ordinary bpffs owned by the
//     unprivileged user through uid/gid MOUNT OPTIONS — never a chown, and
//     never a permission change on a path clawker does not own.
//   - The TOKEN filesystem, which exists only to mint BPF tokens on kernels
//     where this process cannot perform BPF operations on its own. It is
//     private to this mount namespace and holds nothing.
//
// On a rootful deployment the control plane is init-namespace root and
// mounts the pin filesystem itself; no token is involved. On a rootless
// deployment the kernel refuses both the mount and the delegation options
// to this process, and an elevated helper performs them — see the recovery
// flow in internal/controlplane.
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

// TokenMountPath is where the token filesystem is attached. It is private
// to the control plane's mount namespace and holds nothing — no pins ever
// land here — so the location only has to be somewhere writable that no
// other filesystem occupies. cilium/ebpf discovers BPF filesystems by
// filesystem type rather than by path, so it finds this wherever it sits.
const TokenMountPath = "/run/" + consts.NamePrefix + "/token-bpffs"
