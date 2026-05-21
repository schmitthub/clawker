package netlogger

import (
	"context"
	"encoding/json"
	"io"
	"sync"
)

// Sink consumes enriched egress events. Implementations MUST NOT block
// — the processor goroutine is single-threaded and a blocking sink
// stalls the kernel→userspace drain pipeline, refilling the ringbuf
// and causing kernel-fault drops counted in events_drops.
//
// The OTel SDK's BatchProcessor satisfies this contract by design
// (OnEmit returns immediately and batches in its own goroutine).
type Sink interface {
	Emit(ctx context.Context, ev Event)
}

// nopSink discards every event. Tests use it when they only care that
// the pipeline plumbing is correct, not what was emitted.
type nopSink struct{}

// NewNopSink returns a sink that drops every event.
func NewNopSink() Sink { return nopSink{} }

func (nopSink) Emit(context.Context, Event) {}

// stdoutSink writes one JSON-per-line record to its io.Writer. A
// break-glass / test sink — emits the same field set as the
// production OTel-backed sink, just in JSON, so a tail of the
// netlogger pipeline gives operators the same schema they'd see
// after OTel + OpenSearch ingest.
//
// The internal mutex serialises concurrent Emit calls so two events
// don't interleave bytes on the writer — even though the processor
// is single-goroutine in production, tests may invoke Emit
// concurrently and the mutex keeps them honest under -race.
type stdoutSink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewStdoutSink wraps w in a Sink. Pass os.Stdout for break-glass
// debugging, or a *bytes.Buffer in tests.
func NewStdoutSink(w io.Writer) Sink {
	return &stdoutSink{w: w}
}

// stdoutRecord is the JSON shape stdoutSink writes. Kept in lockstep
// with the OTLP attribute set every production sink emits.
type stdoutRecord struct {
	Timestamp   string `json:"timestamp"`
	BPFTsNs     uint64 `json:"bpf_ts_ns"`
	Verdict     string `json:"verdict"`
	CgroupID    uint64 `json:"cgroup_id"`
	ContainerID string `json:"container_id"`
	Agent       string `json:"agent"`
	Project     string `json:"project"`
	DstIP       string `json:"dst_ip"`
	DstPort     uint16 `json:"dst_port"`
	L4Proto     uint8  `json:"l4_proto"`
	IPv6        bool   `json:"ipv6"`
	IPv4Mapped  bool   `json:"ipv4_mapped"`
	DomainHash  uint32 `json:"domain_hash"`
	Domain      string `json:"dst_host"`
}

func (s *stdoutSink) Emit(_ context.Context, ev Event) {
	rec := stdoutRecord{
		Timestamp:   ev.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z"),
		BPFTsNs:     ev.BPFTsNs,
		Verdict:     ev.Verdict.String(),
		CgroupID:    ev.CgroupID,
		ContainerID: ev.ContainerID,
		Agent:       ev.Agent,
		Project:     ev.Project,
		DstPort:     ev.DstPort,
		L4Proto:     ev.L4Proto,
		IPv6:        ev.IsIPv6,
		IPv4Mapped:  ev.IsMapped,
		DomainHash:  ev.DomainHash,
		Domain:      ev.Domain,
	}
	if ev.DstIP.IsValid() {
		rec.DstIP = ev.DstIP.String()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Errors are intentionally dropped: the sink writes to a process-
	// local stream (os.Stdout in break-glass, *bytes.Buffer in tests).
	// A real I/O failure on that surface is not actionable inside the
	// hot path.
	_ = json.NewEncoder(s.w).Encode(rec)
}
