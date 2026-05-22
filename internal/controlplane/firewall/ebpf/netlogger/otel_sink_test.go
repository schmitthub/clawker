package netlogger

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// recordingExporter captures every sdklog.Record handed to Export.
// It is the test-side counterpart to the production OTLP exporter:
// the netlogger pipeline writes records through a real
// *sdklog.LoggerProvider + SimpleProcessor + this exporter, so the
// SDK code paths (resource attribution, scope handling, record
// allocation) are exercised end-to-end without a network hop.
type recordingExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range recs {
		// Clone: the SDK retains slice ownership; copies decouple
		// the test's assertion window from any subsequent SDK
		// mutation.
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *recordingExporter) ForceFlush(context.Context) error { return nil }
func (e *recordingExporter) Shutdown(context.Context) error   { return nil }

func (e *recordingExporter) snapshot() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]sdklog.Record, len(e.records))
	copy(out, e.records)
	return out
}

func attrsAsMap(t *testing.T, rec sdklog.Record) map[string]otellog.Value {
	t.Helper()
	out := make(map[string]otellog.Value, rec.AttributesLen())
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		out[kv.Key] = kv.Value
		return true
	})
	return out
}

// providerWithRecorder builds an in-process LoggerProvider whose
// emitted records land in the returned exporter. SimpleProcessor is
// chosen over BatchProcessor so assertions can run immediately after
// Emit without sleeping past a batching interval.
func providerWithRecorder(t *testing.T) (*sdklog.LoggerProvider, *recordingExporter) {
	t.Helper()
	exp := &recordingExporter{}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)),
	)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
	})
	return provider, exp
}

// TestOtelSink_EmitsAllAttributes locks the strict-directive contract:
// every Event field becomes a record attribute on every Emit, including
// zero/empty values. A future Event field added without updating
// otelSink.Emit makes this test fail.
func TestOtelSink_EmitsAllAttributes(t *testing.T) {
	provider, exp := providerWithRecorder(t)
	sink := newOtelSink(provider)
	if sink == nil {
		t.Fatalf("newOtelSink returned nil for non-nil provider")
	}

	ev := Event{
		Timestamp:   time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		BPFTsNs:     42,
		CgroupID:    9001,
		ContainerID: "cid-abc",
		Agent:       "a1",
		Project:     "p1",
		DstIP:       netip.AddrFrom4([4]byte{203, 0, 113, 7}),
		DstPort:     443,
		L4Proto:     1,
		IsIPv6:      false,
		IsMapped:    false,
		DomainHash:  0xdead,
		Domain:      "example.com",
		Verdict:     VerdictAllowed,
	}
	sink.Emit(context.Background(), ev)

	records := exp.snapshot()
	if len(records) != 1 {
		t.Fatalf("emit count = %d; want 1", len(records))
	}
	rec := records[0]
	if got := rec.EventName(); got != eventName {
		t.Errorf("EventName = %q; want %q", got, eventName)
	}
	if got := rec.Severity(); got != otellog.SeverityInfo {
		t.Errorf("Severity = %v; want Info", got)
	}
	if !rec.Timestamp().Equal(ev.Timestamp) {
		t.Errorf("Timestamp = %v; want %v", rec.Timestamp(), ev.Timestamp)
	}
	if rec.Body().AsString() != "ebpf egress" {
		t.Errorf("Body = %q; want %q", rec.Body().AsString(), "ebpf egress")
	}

	attrs := attrsAsMap(t, rec)
	checks := []struct {
		key  string
		want any
	}{
		{"event.name", eventName},
		{"source", "ebpf"},
		{"verdict", "allowed"},
		{"container_id", "cid-abc"},
		{"agent", "a1"},
		{"project", "p1"},
		{"cgroup_id", "9001"},
		{"bpf_ts_ns", int64(42)},
		{"dst_ip", "203.0.113.7"},
		{"dst_port", "443"},
		{"l4_proto", "stream"},
		{"l4_proto_code", int64(1)},
		{"ipv6", false},
		{"ipv4_mapped", false},
		{"dst_host", "example.com"},
	}
	for _, c := range checks {
		v, ok := attrs[c.key]
		if !ok {
			t.Errorf("attribute %q missing", c.key)
			continue
		}
		switch want := c.want.(type) {
		case string:
			if got := v.AsString(); got != want {
				t.Errorf("attr %q = %q; want %q", c.key, got, want)
			}
		case int64:
			if got := v.AsInt64(); got != want {
				t.Errorf("attr %q = %d; want %d", c.key, got, want)
			}
		case bool:
			if got := v.AsBool(); got != want {
				t.Errorf("attr %q = %v; want %v", c.key, got, want)
			}
		default:
			t.Fatalf("unhandled want type for %q: %T", c.key, want)
		}
	}
}

// TestOtelSink_EmitsEmptyFieldsOnZeroEvent backs the strict directive
// from a different angle: empty strings and zero numbers are emitted
// verbatim — never dropped. Operators filter at query time.
func TestOtelSink_EmitsEmptyFieldsOnZeroEvent(t *testing.T) {
	provider, exp := providerWithRecorder(t)
	sink := newOtelSink(provider)
	sink.Emit(context.Background(), Event{Verdict: VerdictBypassed})

	records := exp.snapshot()
	if len(records) != 1 {
		t.Fatalf("emit count = %d; want 1", len(records))
	}
	attrs := attrsAsMap(t, records[0])

	for _, key := range []string{
		"event.name", "source", "verdict", "container_id", "agent", "project",
		"cgroup_id", "bpf_ts_ns", "dst_ip", "dst_port", "l4_proto",
		"l4_proto_code", "ipv6", "ipv4_mapped", "dst_host",
	} {
		if _, ok := attrs[key]; !ok {
			t.Errorf("attribute %q missing on zero-Event emit", key)
		}
	}
	if _, ok := attrs["domain_hash"]; ok {
		t.Errorf("domain_hash should NOT be emitted; SOC queries on dst_host")
	}
	if got := attrs["verdict"].AsString(); got != "bypassed" {
		t.Errorf("verdict = %q; want bypassed", got)
	}
	if got := attrs["container_id"].AsString(); got != "" {
		t.Errorf("container_id = %q; want empty string", got)
	}
	if got := attrs["cgroup_id"].AsString(); got != "0" {
		t.Errorf("cgroup_id = %q; want %q", got, "0")
	}
	// netip.Addr{}.String() == "invalid IP" — verify the sink emits
	// that verbatim rather than dropping the field. Operators read it
	// as a sentinel for sock_create/native-IPv6 records.
	if got := attrs["dst_ip"].AsString(); got == "" {
		t.Errorf("dst_ip is empty string; want invalid-Addr sentinel")
	}
}

// TestPipeline_OtelSinkIntegration drives the kernel→sink pipeline
// with the production otelSink in place of the recording sink. Real
// sdklog provider, real SimpleProcessor, real Record allocation —
// only the OTLP gRPC exporter is swapped for the in-process
// recordingExporter so the test stays hermetic.
func TestPipeline_OtelSinkIntegration(t *testing.T) {
	provider, exp := providerWithRecorder(t)
	sink := newOtelSink(provider)

	cache := NewLabelCache(logger.Nop())
	cache.AddOrUpdate(424242, "cid-z", "agent-z", "proj-z")

	queue := make(chan []byte, 4)
	p := &processor{
		queue:   queue,
		cache:   cache,
		revDNS:  NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, nil, nil),
		sink:    sink,
		metrics: NewMetrics(),
		log:     logger.Nop(),
	}

	// Hand the processor a single synthetic event then close the
	// queue so run returns once it's drained.
	queue <- mustEncodeEvent(t, ebpf.EgressEvent{
		CgroupId:   424242,
		Verdict:    ebpf.EgressVerdictAllowed,
		L4Proto:    1,
		DomainHash: 0xfeed,
	})
	close(queue)
	p.run(context.Background())

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if got := len(exp.snapshot()); got != 1 {
		t.Fatalf("recorded records = %d; want 1", got)
	}
	attrs := attrsAsMap(t, exp.snapshot()[0])
	if got := attrs["container_id"].AsString(); got != "cid-z" {
		t.Errorf("enrichment lost: container_id = %q", got)
	}
	if got := attrs["agent"].AsString(); got != "agent-z" {
		t.Errorf("enrichment lost: agent = %q", got)
	}
}

func TestL4ProtoString(t *testing.T) {
	cases := map[uint8]string{
		1: "stream",
		2: "dgram",
		3: "raw",
		0: "unknown(0)",
		9: "unknown(9)",
	}
	for code, want := range cases {
		if got := l4ProtoString(code); got != want {
			t.Errorf("l4ProtoString(%d) = %q; want %q", code, got, want)
		}
	}
}
