package netlogger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"

	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
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
		return "allowed"
	case VerdictDenied:
		return "denied"
	case VerdictBypassed:
		return "bypassed"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(v))
	}
}

// Event is the enriched record the processor emits to a Sink. Lifetime
// is per-Emit; the processor reuses the underlying byte slice for the
// next record so sinks MUST copy any fields they retain across the
// Emit boundary.
//
// Strict directive: every field on this struct lands as an OTLP
// attribute on every emitted record. Empty/zero values are emitted
// verbatim; sinks never drop a field because its value is zero.
// Adding a field is a contract change — update every Sink
// implementation and any asserters in the same commit.
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
	// clawker-net IP, redundant with attribution — not carried.
	DstIP    netip.Addr
	DstPort  uint16
	L4Proto  uint8
	IsIPv6   bool
	IsMapped bool

	// Domain attribution. DomainHash is the BPF-side FNV-1a hash of
	// the resolved hostname; Domain is the reverse-DNS lookup result
	// (empty when ReverseDNSMap has no entry for the hash, or when
	// the connect was direct-IP).
	DomainHash uint32
	Domain     string

	Verdict Verdict
}

// parseEvent decodes a raw ringbuf record into the bpf2go-generated
// EgressEvent struct using binary.NativeEndian (host byte order — the
// BPF writer and userspace reader share endianness, and the
// bpf2go-generated struct carries structs.HostLayout for C-compatible
// offsets).
//
// The DstIp field is treated as network byte order per the BPF
// endianness convention (matches ctx->user_ip4 in the kernel). All
// other fields are host byte order.
//
// Returns a fully-populated Event with DomainHash and CgroupID set;
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
		Timestamp:  time.Now(),
		BPFTsNs:    rec.TsNs,
		CgroupID:   rec.CgroupId,
		DstPort:    rec.DstPort,
		L4Proto:    rec.L4Proto,
		IsIPv6:     rec.Flags&ebpf.EgressFlagIPv6 != 0,
		IsMapped:   rec.Flags&ebpf.EgressFlagIPv4Mapped != 0,
		DomainHash: rec.DomainHash,
		Verdict:    Verdict(rec.Verdict),
	}
	if rec.DstIp != 0 {
		// BPF stores DstIp in network byte order (matches ctx->user_ip4).
		// ebpf.Uint32ToIP unpacks it into 4 network-order bytes, which
		// netip.AddrFrom4 accepts directly.
		ip := ebpf.Uint32ToIP(rec.DstIp).To4()
		if ip != nil {
			ev.DstIP = netip.AddrFrom4([4]byte{ip[0], ip[1], ip[2], ip[3]})
		}
	}
	return ev, nil
}
