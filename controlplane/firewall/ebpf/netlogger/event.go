package netlogger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"

	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/consts"
)

// Verdict mirrors the BPF egress_verdict enum. Kept as a typed alias on
// uint8 so callers get exhaustive switch warnings.
type Verdict uint8

const (
	VerdictAllowed  Verdict = Verdict(ebpf.EgressVerdictAllowed)
	VerdictDenied   Verdict = Verdict(ebpf.EgressVerdictDenied)
	VerdictBypassed Verdict = Verdict(ebpf.EgressVerdictBypassed)
)

// String renders the verdict as the OTLP attribute value
// ("allowed"/"denied"/"bypassed").
func (v Verdict) String() string {
	switch v {
	case VerdictAllowed:
		return consts.VerdictAllowed
	case VerdictDenied:
		return consts.VerdictDenied
	case VerdictBypassed:
		return consts.VerdictBypassed
	default:
		return fmt.Sprintf("unknown(%d)", uint8(v))
	}
}

// EmitSite mirrors the (Flags & EgressEmitMask) enum from BPF —
// identifies which cgroup BPF program submitted the event. The OTel
// sink uses this to set event.name per record kind so dashboards can
// filter `event.name:ebpf.egress.connect AND action:denied` etc.
// without having to look at flag bits.
type EmitSite uint8

const (
	EmitSiteConnect    EmitSite = EmitSite(ebpf.EgressEmitConnect >> ebpf.EgressEmitShift)
	EmitSiteSendmsg    EmitSite = EmitSite(ebpf.EgressEmitSendmsg >> ebpf.EgressEmitShift)
	EmitSiteSockCreate EmitSite = EmitSite(ebpf.EgressEmitSockCreate >> ebpf.EgressEmitShift)
)

// EventName returns the OTel event.name attribute value for this emit
// site ("ebpf.egress.connect" / "ebpf.egress.sendmsg" /
// "ebpf.egress.sock_create"). Unknown values fall through to a
// debuggable sentinel.
func (s EmitSite) EventName() string {
	switch s {
	case EmitSiteConnect:
		return "ebpf.egress.connect"
	case EmitSiteSendmsg:
		return "ebpf.egress.sendmsg"
	case EmitSiteSockCreate:
		return "ebpf.egress.sock_create"
	default:
		return fmt.Sprintf("ebpf.egress.unknown(%d)", uint8(s))
	}
}

// Event is the enriched record the processor emits to a Sink. It is a
// self-contained value: every field is either a scalar, a value-typed
// struct (netip.Addr, time.Time), or a string with independent
// ownership (decoded from the binary record or sourced from
// LabelCache / ReverseDNSMap). The underlying ringbuf bytes are
// freshly allocated per record by reader.drain and dropped after
// parseEvent returns, so sinks may retain Event or any of its fields
// without copying.
//
// Strict directive: every field on this struct lands as an OTLP
// attribute on every emitted record, except fields BPF does not carry
// on the originating code path (DstIP / DstPort on sock_create —
// NoDst=true; DstIP on native-IPv6 paths historically — now captured
// in full). Those are omitted entirely so operators partition via
// _exists_:attributes.<field>. Adding a field is a contract change —
// update every Sink implementation and any asserters in the same
// commit.
type Event struct {
	// Timestamp is the userspace receive time. BPF stamps
	// bpf_ktime_get_ns (CLOCK_MONOTONIC since boot) into the raw
	// record, but the kernel→userspace lag is well below OTel's
	// batching window, so we anchor on time.Now() at parse instead
	// of plumbing a boot-offset that would drift under
	// suspend/resume. The raw BPFTsNs is preserved verbatim for
	// forensic ordering.
	Timestamp time.Time
	BPFTsNs   uint64

	// Trust-anchored kernel attribution.
	CgroupID uint64

	// Userspace-enriched container identity. Empty when the
	// LabelCache has no entry for CgroupID (cache cold or a
	// container die/destroy already evicted the binding).
	ContainerID string
	Agent       string
	Project     string

	// Network 4-tuple destination side. SrcIP is the container's own
	// clawker network IP, redundant with attribution — not carried.
	//
	// DstIP is invalid (zero netip.Addr) when NoDst=true (sock_create
	// events have no destination). The sink omits the OTLP dst_ip
	// attribute in that case so OS Discover renders an empty cell and
	// _exists_:attributes.dst_ip filters cleanly.
	DstIP    netip.Addr
	DstPort  uint16
	L4Proto  uint8
	IsIPv6   bool
	IsMapped bool
	NoDst    bool

	// EmitSite identifies which BPF program submitted the event.
	// Drives event.name on the OTel record so dashboards can filter
	// per record kind without bit-twiddling Flags.
	EmitSite EmitSite

	// Domain attribution. Identity is the CP-allocated route identity of
	// the resolved hostname; Domain is the reverse-DNS lookup result
	// (empty when ReverseDNSMap has no entry for the hash, or when
	// the connect was direct-IP).
	Identity uint32
	Domain   string

	Verdict Verdict
}

// parseEvent decodes a raw ringbuf record into the bpf2go-generated
// EgressEvent struct using binary.NativeEndian (host byte order — the
// BPF writer and userspace reader share endianness, and the
// bpf2go-generated struct carries structs.HostLayout for C-compatible
// offsets).
//
// DstIp is a 16-byte slot in network byte order, discriminated by the
// flags bitmask (matches Cilium trace_notify and Tetragon tuple_type
// conventions):
//   - EgressFlagNoDst set: sock_create event with no destination —
//     leave Event.DstIP as the invalid netip.Addr{}.
//   - EgressFlagIPv6 set: native IPv6 — decode all 16 bytes as the
//     v6 destination.
//   - Otherwise (pure v4 or v4-mapped): low 4 bytes carry the v4
//     destination; rest is zero.
//
// Other fields are host byte order.
//
// Returns a fully-populated Event with Identity and CgroupID set;
// userspace enrichment (ContainerID/Agent/Project/Domain) is layered
// on by the processor.
func parseEvent(raw []byte) (Event, error) {
	if len(raw) < binary.Size(ebpf.EgressEvent{}) {
		return Event{}, fmt.Errorf("ringbuf record %d bytes; want >= %d", len(raw), binary.Size(ebpf.EgressEvent{}))
	}
	var rec ebpf.EgressEvent
	if err := binary.Read(bytes.NewReader(raw), binary.NativeEndian, &rec); err != nil {
		return Event{}, fmt.Errorf("decode egress_event: %w", err)
	}

	ev := Event{
		Timestamp: time.Now(),
		BPFTsNs:   rec.TsNs,
		CgroupID:  rec.CgroupId,
		DstPort:   rec.DstPort,
		L4Proto:   rec.L4Proto,
		IsIPv6:    rec.Flags&ebpf.EgressFlagIPv6 != 0,
		IsMapped:  rec.Flags&ebpf.EgressFlagIPv4Mapped != 0,
		NoDst:     rec.Flags&ebpf.EgressFlagNoDst != 0,
		EmitSite:  EmitSite((rec.Flags & ebpf.EgressEmitMask) >> ebpf.EgressEmitShift),
		Identity:  rec.Identity,
		Verdict:   Verdict(rec.Verdict),
	}
	switch {
	case ev.NoDst:
		// Socket creation event — no destination exists.
		// Event.DstIP stays zero/invalid; sink omits the attribute.
	case ev.IsIPv6:
		// Full 16-byte native v6 address, network order. AddrFrom16
		// accepts the bytes directly; Unmap normalizes any v4-mapped
		// form that slipped through (shouldn't happen given the BPF
		// flag wiring but defensive).
		ev.DstIP = netip.AddrFrom16(rec.DstIp).Unmap()
	default:
		// Pure v4 or v4-mapped — low 4 bytes are the v4 dest in
		// network order. AddrFrom4 accepts those bytes verbatim.
		ev.DstIP = netip.AddrFrom4([4]byte{rec.DstIp[0], rec.DstIp[1], rec.DstIp[2], rec.DstIp[3]})
	}
	return ev, nil
}
