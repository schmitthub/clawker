// Package delegation is the contract between the two halves of clawker's
// BPF filesystem setup: the unprivileged control plane inside its user
// namespace, and the elevated one-shot helper that performs the parts the
// kernel reserves for init-namespace CAP_SYS_ADMIN.
//
// It exists as its own package for one reason: the helper runs as root, and
// the only thing it should link is the handful of syscalls it makes. Reading
// these constants out of the ebpf package would drag the whole loader —
// cilium/ebpf plus the compiled BPF objects — into a root-run binary that
// loads nothing.
//
// Nothing here is negotiated at runtime. The masks are compile-time policy on
// both sides so the privileged half never grants what it was asked for, only
// what it was built for.
package delegation

import "strconv"

// Delegation masks: exactly the BPF operations clawker's own object needs,
// enumerated rather than delegated wholesale with "any". Every entry is
// derived from bpf/clawker.c and bpf/common.h, with one deliberate
// exception called out below.
const (
	// Cmds are the three commands cilium/ebpf injects a token into.
	Cmds = "map_create:prog_load:btf_load"

	// Maps is every __uint(type, ...) in bpf/common.h — plus array, which
	// clawker's object never declares.
	//
	// The array entry is NOT redundant. cilium/ebpf's "object names" feature
	// probe creates a BPF_MAP_TYPE_ARRAY map; when the mask refuses it, the
	// library concludes the kernel has no object-name support and silently
	// drops the name from every map and program it creates afterwards.
	// Measured on a rootless host: without array, 0 of 10 maps and 0 of 9
	// programs kept their kernel names; with it, all of them did. Nothing
	// errors — pinning still works, because pin paths come from the spec
	// name — so the only symptom is that every clawker object shows up
	// anonymous under bpftool and in verifier output.
	//
	// The rule this encodes: the mask must admit what the LIBRARY probes
	// with, not only what our object declares.
	Maps = "hash:lru_hash:percpu_hash:percpu_array:ringbuf:array"

	// Progs covers both program types the object ships: cgroup/connect{4,6},
	// sendmsg{4,6}, recvmsg{4,6} and getpeername{4,6} are CGROUP_SOCK_ADDR;
	// cgroup/sock_create is CGROUP_SOCK.
	//
	// socket_filter and kprobe are deliberately absent even though cilium
	// probes with them (haveBPFToBPFCalls, haveProgramExtInfos and
	// haveProbeReadKernel all fail without them). The real object loads
	// correctly regardless, and kprobe is a tracing grant — not a price
	// worth paying for richer verifier output.
	Progs = "cgroup_sock_addr:cgroup_sock"

	// Attachs are the nine attach points those programs use. Loading
	// succeeds without these; attaching does not.
	Attachs = "cgroup_inet4_connect:cgroup_inet6_connect:" +
		"cgroup_udp4_sendmsg:cgroup_udp6_sendmsg:" +
		"cgroup_udp4_recvmsg:cgroup_udp6_recvmsg:" +
		"cgroup_inet4_getpeername:cgroup_inet6_getpeername:" +
		"cgroup_inet_sock_create"
)

// Param is one BPF filesystem parameter and the value clawker sets it to.
type Param struct {
	Name  string
	Value string
}

// Params are the four filesystem parameters that must be set on the
// filesystem context before it is instantiated. Both halves iterate this
// rather than each spelling out the four names.
func Params() []Param {
	return []Param{
		{Name: "delegate_cmds", Value: Cmds},
		{Name: "delegate_maps", Value: Maps},
		{Name: "delegate_progs", Value: Progs},
		{Name: "delegate_attachs", Value: Attachs},
	}
}

// AckOK is the single byte the helper writes once every job succeeded.
// Anything else — including a closed connection — means it failed and said
// so, rather than leaving the control plane blocked until its deadline.
const AckOK = 'k'

// SocketName is the unix socket the control plane serves the BPF filesystem
// context on while it waits. It lives in the firewall data directory, which
// is bind-mounted from the host, because the helper runs out there: a file
// descriptor cannot cross a process boundary any other way.
const SocketName = "bpffs-handoff.sock"

// FSType is the kernel's name for the BPF filesystem.
const FSType = "bpf"

// OwnerParams are the filesystem parameters that set the filesystem's
// ownership at creation. The kernel added uid/gid/mode to bpffs for exactly
// this, so the filesystem is born owned by the unprivileged control plane
// instead of being chowned afterwards — and no permission on a path clawker
// does not own is ever touched. The helper reads uid and gid off the handoff
// socket's peer credentials, never from configuration.
func OwnerParams(uid, gid int) []Param {
	return []Param{
		{Name: "mode", Value: "0700"},
		{Name: "uid", Value: strconv.Itoa(uid)},
		{Name: "gid", Value: strconv.Itoa(gid)},
	}
}
