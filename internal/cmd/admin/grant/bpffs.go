//go:build linux

package grant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cilium/ebpf"
	"github.com/schmitthub/clawker/internal/consts"
	"golang.org/x/sys/unix"
)

// BPFFS delegation mount options. Each takes a colon-separated list or a hex
// mask; hex is used here so delegation does not depend on the running kernel
// shipping BTF for symbolic name lookup.
const (
	bpffsName          = "bpf"
	bpffsDirName       = "bpffs"
	optDelegateCmds    = "delegate_cmds"
	optDelegateMaps    = "delegate_maps"
	optDelegateProgs   = "delegate_progs"
	optDelegateAttachs = "delegate_attachs"
	delegateMark       = "delegate_"
)

// bpf() commands the loader issues with a token. Values are kernel UAPI
// (enum bpf_cmd); only these three consult the token's allowed-command mask.
const (
	bpfCmdMapCreate = 0
	bpfCmdProgLoad  = 5
	bpfCmdBTFLoad   = 18
)

// delegatedMasks are the bpf() commands, map types, program types and attach
// types clawker uses, and nothing else. A token minted from the delegated
// filesystem carries exactly these.
func delegatedMasks() map[string]uint64 {
	return map[string]uint64{
		optDelegateCmds: bit(bpfCmdMapCreate) | bit(bpfCmdProgLoad) | bit(bpfCmdBTFLoad),
		optDelegateMaps: bit(ebpf.Hash) | bit(ebpf.LRUHash) | bit(ebpf.PerCPUHash) |
			bit(ebpf.PerCPUArray) | bit(ebpf.RingBuf),
		optDelegateProgs: bit(ebpf.CGroupSockAddr) | bit(ebpf.CGroupSock),
		optDelegateAttachs: bit(ebpf.AttachCGroupInet4Connect) | bit(ebpf.AttachCGroupInet6Connect) |
			bit(ebpf.AttachCGroupUDP4Sendmsg) | bit(ebpf.AttachCGroupUDP6Sendmsg) |
			bit(ebpf.AttachCGroupUDP4Recvmsg) | bit(ebpf.AttachCGroupUDP6Recvmsg) |
			bit(ebpf.AttachCgroupInet4GetPeername) | bit(ebpf.AttachCgroupInet6GetPeername) |
			bit(ebpf.AttachCGroupInetSockCreate),
	}
}

// bit returns the delegation mask bit for a kernel enum value.
func bit[T ~uint32 | ~int](value T) uint64 {
	return uint64(1) << uint64(value)
}

// bpffsPath is where the delegating filesystem attaches: beside clawker's
// other state, since eBPF serves both egress enforcement and monitoring.
func bpffsPath() string {
	return filepath.Join(consts.DataDir(), bpffsDirName)
}

// alreadyDelegated reports whether a delegating filesystem is already attached
// at path.
func alreadyDelegated(path string) (bool, error) {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read mount table: %w", err)
	}

	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		// Past the optional-fields separator: fstype, source, super options.
		if separator < 5 || len(fields) < separator+4 {
			continue
		}
		if fields[4] != path || fields[separator+1] != bpffsName {
			continue
		}
		if strings.Contains(fields[separator+3], delegateMark) {
			return true, nil
		}
	}
	return false, nil
}

// grantBPFFS attaches a BPFFS carrying clawker's delegated eBPF privileges, so
// the programs and pins clawker needs can be created without root. It reports
// whether anything was attached; an already-delegated filesystem is left alone.
func grantBPFFS() (bool, error) {
	path := bpffsPath()

	delegated, err := alreadyDelegated(path)
	if err != nil {
		return false, err
	}
	if delegated {
		return false, nil
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, fmt.Errorf("create %s: %w", path, err)
	}

	fsfd, err := unix.Fsopen(bpffsName, unix.FSOPEN_CLOEXEC)
	if err != nil {
		return false, fmt.Errorf("open %s filesystem context: %w", bpffsName, err)
	}
	defer unix.Close(fsfd)

	masks := delegatedMasks()
	for _, opt := range []string{optDelegateCmds, optDelegateMaps, optDelegateProgs, optDelegateAttachs} {
		value := fmt.Sprintf("%#x", masks[opt])
		if err := unix.FsconfigSetString(fsfd, opt, value); err != nil {
			return false, fmt.Errorf("set %s=%s: %w", opt, value, err)
		}
	}

	if err := unix.FsconfigCreate(fsfd); err != nil {
		return false, fmt.Errorf("create delegated %s: %w", bpffsName, err)
	}

	mntfd, err := unix.Fsmount(fsfd, unix.FSMOUNT_CLOEXEC, 0)
	if err != nil {
		return false, fmt.Errorf("mount delegated %s: %w", bpffsName, err)
	}
	defer unix.Close(mntfd)

	if err := unix.MoveMount(mntfd, "", unix.AT_FDCWD, path, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return false, fmt.Errorf("attach delegated %s at %s: %w", bpffsName, path, err)
	}
	return true, nil
}
