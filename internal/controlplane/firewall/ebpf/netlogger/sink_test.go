package netlogger

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"
)

func TestStdoutSink_EmitsAllFields(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSink(&buf)
	ev := Event{
		Timestamp:   time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		BPFTsNs:     42,
		CgroupID:    9001,
		ContainerID: "abc",
		Agent:       "a1",
		Project:     "p1",
		DstIP:       netip.AddrFrom4([4]byte{203, 0, 113, 7}),
		DstPort:     443,
		L4Proto:     1,
		IsIPv6:      false,
		IsMapped:    false,
		DomainHash:  0xdead,
		Domain:      "",
		Verdict:     VerdictAllowed,
	}
	sink.Emit(context.Background(), ev)

	var got stdoutRecord
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode emitted JSON: %v\nraw=%q", err, buf.String())
	}
	// Strict directive: every Event field lands as an attribute.
	// The assertion is intentionally per-field rather than reflect-
	// equal so a future Event field is forced into the sink update.
	want := stdoutRecord{
		Timestamp:   "2026-05-21T12:00:00.000000000Z",
		BPFTsNs:     42,
		Verdict:     "allowed",
		CgroupID:    9001,
		ContainerID: "abc",
		Agent:       "a1",
		Project:     "p1",
		DstIP:       "203.0.113.7",
		DstPort:     443,
		L4Proto:     1,
		IPv6:        false,
		IPv4Mapped:  false,
		DomainHash:  0xdead,
		Domain:      "",
	}
	if got != want {
		t.Errorf("got = %+v\nwant = %+v", got, want)
	}
}

func TestStdoutSink_OmitsInvalidDstIP(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSink(&buf)
	// DstIP zero-value Addr (e.g. native-IPv6 or sock_create record).
	// The sink must serialise an empty string, not "0.0.0.0".
	sink.Emit(context.Background(), Event{Verdict: VerdictBypassed})
	var got stdoutRecord
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DstIP != "" {
		t.Errorf("DstIP = %q; want empty for zero-Addr events", got.DstIP)
	}
}
