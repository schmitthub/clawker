package netlogger

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"

	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
)

// TestParseEvent_RoundTrip is the parser's load-bearing fixture: it
// serialises a known EgressEvent via the same NativeEndian path
// production uses, decodes it back, and asserts every field round-trips.
// The IPv4 case pins the network-byte-order convention (kernel writes
// ctx->user_ip4 in net order; parse converts via ebpf.Uint32ToIP) by
// constructing the address via ebpf.IPToUint32 and checking the
// netip.Addr round-trips byte-equal.
func TestParseEvent_RoundTrip(t *testing.T) {
	ip := net.IPv4(203, 0, 113, 7)
	in := ebpf.EgressEvent{
		TsNs:       1234567890,
		CgroupId:   424242,
		DomainHash: 0xdeadbeef,
		DstIp:      ebpf.IPToUint32(ip),
		DstPort:    443,
		Verdict:    ebpf.EgressVerdictAllowed,
		Flags:      0,
		L4Proto:    1, // SOCK_STREAM
	}
	raw := mustEncode(t, in)

	got, err := parseEvent(raw)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if got.CgroupID != in.CgroupId {
		t.Errorf("CgroupID = %d; want %d", got.CgroupID, in.CgroupId)
	}
	if got.BPFTsNs != in.TsNs {
		t.Errorf("BPFTsNs = %d; want %d", got.BPFTsNs, in.TsNs)
	}
	if got.DomainHash != in.DomainHash {
		t.Errorf("DomainHash = %x; want %x", got.DomainHash, in.DomainHash)
	}
	want := netip.AddrFrom4([4]byte{203, 0, 113, 7})
	if got.DstIP != want {
		t.Errorf("DstIP = %v; want %v", got.DstIP, want)
	}
	if got.DstPort != 443 {
		t.Errorf("DstPort = %d; want 443", got.DstPort)
	}
	if got.Verdict != VerdictAllowed {
		t.Errorf("Verdict = %v; want %v", got.Verdict, VerdictAllowed)
	}
	if got.L4Proto != 1 {
		t.Errorf("L4Proto = %d; want 1", got.L4Proto)
	}
	if got.IsIPv6 || got.IsMapped {
		t.Errorf("flags decoded incorrectly: IsIPv6=%v IsMapped=%v", got.IsIPv6, got.IsMapped)
	}
}

// TestParseEvent_Verdicts pins the enum mapping so a future BPF-side
// renumbering surfaces here before the OTel sink starts emitting
// stale verdict strings.
func TestParseEvent_Verdicts(t *testing.T) {
	cases := []struct {
		name    string
		raw     uint8
		want    Verdict
		wantStr string
	}{
		{"allowed", ebpf.EgressVerdictAllowed, VerdictAllowed, "allowed"},
		{"denied", ebpf.EgressVerdictDenied, VerdictDenied, "denied"},
		{"bypassed", ebpf.EgressVerdictBypassed, VerdictBypassed, "bypassed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := parseEvent(mustEncode(t, ebpf.EgressEvent{Verdict: tc.raw}))
			if err != nil {
				t.Fatalf("parseEvent: %v", err)
			}
			if ev.Verdict != tc.want {
				t.Errorf("Verdict = %v; want %v", ev.Verdict, tc.want)
			}
			if got := ev.Verdict.String(); got != tc.wantStr {
				t.Errorf("Verdict.String = %q; want %q", got, tc.wantStr)
			}
		})
	}
}

// TestParseEvent_Flags pins the IPv6 / IPv4-mapped flag decoding.
// EgressFlagIPv6 → IsIPv6, EgressFlagIPv4Mapped → IsMapped. The two
// can co-occur on a dual-stack connect.
func TestParseEvent_Flags(t *testing.T) {
	cases := []struct {
		name     string
		flags    uint8
		isIPv6   bool
		isMapped bool
	}{
		{"none", 0, false, false},
		{"ipv6", ebpf.EgressFlagIPv6, true, false},
		{"mapped", ebpf.EgressFlagIPv4Mapped, false, true},
		{"both", ebpf.EgressFlagIPv6 | ebpf.EgressFlagIPv4Mapped, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := parseEvent(mustEncode(t, ebpf.EgressEvent{Flags: tc.flags}))
			if err != nil {
				t.Fatalf("parseEvent: %v", err)
			}
			if ev.IsIPv6 != tc.isIPv6 || ev.IsMapped != tc.isMapped {
				t.Errorf("got IsIPv6=%v IsMapped=%v; want IsIPv6=%v IsMapped=%v",
					ev.IsIPv6, ev.IsMapped, tc.isIPv6, tc.isMapped)
			}
		})
	}
}

// TestParseEvent_ZeroDstIPIsInvalid pins the "DstIp == 0" path: the
// kernel sets DstIp=0 for native IPv6 + sock_create records. parseEvent
// must leave the netip.Addr at its zero value rather than serialising
// 0.0.0.0 (which would mislead operators querying for the wildcard
// address).
func TestParseEvent_ZeroDstIPIsInvalid(t *testing.T) {
	ev, err := parseEvent(mustEncode(t, ebpf.EgressEvent{DstIp: 0}))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if ev.DstIP.IsValid() {
		t.Errorf("DstIP = %v; want zero-value Addr for native-IPv6/sock_create records", ev.DstIP)
	}
}

// TestParseEvent_TruncatedRecord guards the bytes-len check.
func TestParseEvent_TruncatedRecord(t *testing.T) {
	if _, err := parseEvent([]byte{0x01, 0x02}); err == nil {
		t.Fatalf("expected error on truncated record")
	}
}

func mustEncode(t *testing.T, ev ebpf.EgressEvent) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.NativeEndian, &ev); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}
