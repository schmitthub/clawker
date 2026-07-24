package netlogger

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// recordingSink captures every Emit call for assertion. Safe under
// concurrent Emit (the processor is single-goroutine in production
// but the test invokes Emit directly from multiple goroutines in
// the concurrency tests).
type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingSink) Emit(_ context.Context, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingSink) snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func TestProcessor_ParsesEnrichesEmits(t *testing.T) {
	cache := NewLabelCache(nil)
	cache.AddOrUpdate(424242, "abc", "agent-x", "proj-y")

	queue := make(chan []byte, 4)
	sink := &recordingSink{}
	metrics := NewMetrics()
	p := &processor{
		queue:   queue,
		cache:   cache,
		revDNS:  NewReverseDNSMapWithWalk(func(func(ebpf.RouteIdentity)) error { return nil }, nil, nil),
		sink:    sink,
		metrics: metrics,
		log:     logger.Nop(),
	}

	raw := mustEncodeEvent(t, ebpf.EgressEvent{
		CgroupId: 424242,
		DstIp:    ebpf.IPToBytes16(net.IPv4(203, 0, 113, 9)),
		DstPort:  443,
		Verdict:  ebpf.EgressVerdictAllowed,
		L4Proto:  1,
		Identity: 0xbeef,
	})
	queue <- raw
	close(queue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.run(ctx)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("emit count = %d; want 1", len(events))
	}
	got := events[0]
	if got.ContainerID != "abc" || got.Agent != "agent-x" || got.Project != "proj-y" {
		t.Errorf("enrichment missing: cid=%q agent=%q project=%q", got.ContainerID, got.Agent, got.Project)
	}
	if got.DstPort != 443 || got.Verdict != VerdictAllowed {
		t.Errorf("decode fields wrong: dst_port=%d verdict=%v", got.DstPort, got.Verdict)
	}
}

func TestProcessor_ParseErrorIncrementsCounterAndSkips(t *testing.T) {
	queue := make(chan []byte, 4)
	sink := &recordingSink{}
	metrics := NewMetrics()
	p := &processor{
		queue:   queue,
		cache:   NewLabelCache(nil),
		revDNS:  NewReverseDNSMapWithWalk(func(func(ebpf.RouteIdentity)) error { return nil }, nil, nil),
		sink:    sink,
		metrics: metrics,
		log:     logger.Nop(),
	}
	queue <- []byte{0x01} // truncated
	queue <- mustEncodeEvent(t, ebpf.EgressEvent{CgroupId: 1})
	close(queue)
	p.run(context.Background())

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("emit count = %d; want 1 (truncated record must skip)", len(events))
	}
	if events[0].CgroupID != 1 {
		t.Errorf("emitted event = %+v; want CgroupID=1", events[0])
	}
}

func TestProcessor_CacheMissEmitsEmptyAttribution(t *testing.T) {
	queue := make(chan []byte, 4)
	sink := &recordingSink{}
	metrics := NewMetrics()
	p := &processor{
		queue:   queue,
		cache:   NewLabelCache(nil), // empty
		revDNS:  NewReverseDNSMapWithWalk(func(func(ebpf.RouteIdentity)) error { return nil }, nil, nil),
		sink:    sink,
		metrics: metrics,
		log:     logger.Nop(),
	}
	queue <- mustEncodeEvent(t, ebpf.EgressEvent{CgroupId: 999, Verdict: ebpf.EgressVerdictDenied})
	close(queue)
	p.run(context.Background())

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("emit count = %d; want 1", len(events))
	}
	ev := events[0]
	// Strict directive: empty labels are emitted verbatim, never
	// dropped. The processor sets them to "" on cache miss.
	if ev.ContainerID != "" || ev.Agent != "" || ev.Project != "" {
		t.Errorf("cache-miss enrichment leaked stale values: %+v", ev)
	}
	if ev.Verdict != VerdictDenied {
		t.Errorf("verdict misdecoded on cache miss: %v", ev.Verdict)
	}
}

// TestPipeline_EndToEnd drives the kernel→sink pipeline (reader →
// queue → processor → sink) with a recording sink, exercising the
// wiring without touching the OTel SDK. The OTel sink itself is
// covered by otel_sink_test.go.
func TestPipeline_EndToEnd(t *testing.T) {
	cache := NewLabelCache(nil)
	cache.AddOrUpdate(7, "ABC", "agent-1", "project-1")

	queue := make(chan []byte, 4)
	src := &fakeRingbuf{records: [][]byte{
		mustEncodeEvent(t, ebpf.EgressEvent{
			CgroupId: 7,
			DstIp:    ebpf.IPToBytes16(net.IPv4(192, 0, 2, 33)),
			DstPort:  80,
			Verdict:  ebpf.EgressVerdictAllowed,
			L4Proto:  1,
			Identity: 0xfeed,
		}),
	}}
	metrics := NewMetrics()
	r := &reader{src: src, queue: queue, metrics: metrics, log: logger.Nop()}

	sink := &recordingSink{}
	p := &processor{
		queue:   queue,
		cache:   cache,
		revDNS:  NewReverseDNSMapWithWalk(func(func(ebpf.RouteIdentity)) error { return nil }, nil, nil),
		sink:    sink,
		metrics: metrics,
		log:     logger.Nop(),
	}

	done := make(chan struct{})
	go func() { r.drain(); close(queue); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("reader did not exit")
	}
	p.run(context.Background())

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("emit count = %d; want 1", len(events))
	}
	rec := events[0]
	if rec.Verdict != VerdictAllowed || rec.ContainerID != "ABC" ||
		rec.Agent != "agent-1" || rec.Project != "project-1" ||
		rec.DstIP.String() != "192.0.2.33" || rec.DstPort != 80 ||
		rec.Identity != 0xfeed {
		t.Errorf("end-to-end record fields wrong: %+v", rec)
	}
}

func mustEncodeEvent(t *testing.T, ev ebpf.EgressEvent) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.NativeEndian, &ev); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}
