package pubsub_test

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
)

// fooEvent and barEvent are two distinct domain payload types used to prove
// the bus is compile-time type-safe and that topics are isolated by type.
type fooEvent struct {
	Action string
	N      int
}

type barEvent struct {
	Name string
}

func newTopic[T any](t *testing.T, opts ...pubsub.Option) *pubsub.Topic[T] {
	t.Helper()
	top, err := pubsub.NewTopic[T](logger.Nop(), opts...)
	if err != nil {
		t.Fatalf("NewTopic: unexpected error: %v", err)
	}
	return top
}

// waitFor polls cond until true or the deadline elapses. Used to synchronize
// on asynchronous per-subscriber delivery without sleeping a fixed duration.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// (a) Typed publish->subscribe round-trip: the consumer reads e.Payload
// natively with ZERO type assertion. If the API surfaced `any`, this would
// not compile — that is the point.
func TestTopic_TypedRoundTrip(t *testing.T) {
	top := newTopic[fooEvent](t)

	var mu sync.Mutex
	var got []fooEvent
	top.Subscribe(func(e pubsub.Event[fooEvent]) {
		// Native field access, no assertion. e.Payload is fooEvent.
		mu.Lock()
		got = append(got, e.Payload)
		mu.Unlock()
	})

	if ok := top.Publish(pubsub.Event[fooEvent]{
		ID:        "evt-1",
		Timestamp: time.Now().UnixNano(),
		Source:    "test",
		Payload:   fooEvent{Action: "create", N: 7},
	}); !ok {
		t.Fatal("Publish returned false on an empty buffer; want true")
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if got[0].Action != "create" || got[0].N != 7 {
		t.Fatalf("payload mismatch: got %+v", got[0])
	}
}

// (b) SECURITY-CRITICAL: a panicking subscriber is contained. The bus
// survives, the PANICKING subscriber's own drain loop keeps delivering across
// every event, OTHER subscribers on the SAME topic and on OTHER topics still
// receive events, and a recovered-panic counter is observed.
//
// This test MUST go RED against any impl that does not recover per-event inside
// the panicker's own drain loop. The load-bearing assertion is panicSeen==n:
// the panicking subscriber must be re-entered for every one of the n events.
// If deliver's recover is removed, the panic escapes deliver; the only thing
// standing between the panic and the goroutine's death is drain()'s own
// loop-level recover, which fires ONCE and then the `for event := range
// sub.queue` loop is gone — so the panicker is re-entered for event 0 only,
// panicSeen freezes at 1, and this test goes red on substance (the panicker's
// drain-loop survival), not merely on a relocatable counter. The healthy
// subscribers run on independent goroutines and would survive even a broken
// containment, so they are a secondary, not the primary, signal.
func TestTopic_PanicContained(t *testing.T) {
	fooTop := newTopic[fooEvent](t)
	barTop := newTopic[barEvent](t)

	var panicSeen, healthyFoo, healthyBar atomic.Int64

	// Panicking subscriber on the foo topic. It records every invocation of
	// itself BEFORE panicking, so panicSeen reaching n proves its own drain
	// loop survived each contained panic and re-entered the handler n times.
	fooTop.Subscribe(func(e pubsub.Event[fooEvent]) {
		panicSeen.Add(1)
		panic("subscriber boom")
	})
	// Healthy subscriber on the SAME topic — must still receive.
	fooTop.Subscribe(func(e pubsub.Event[fooEvent]) {
		healthyFoo.Add(1)
	})
	// Healthy subscriber on a DIFFERENT topic — must be unaffected.
	barTop.Subscribe(func(e pubsub.Event[barEvent]) {
		healthyBar.Add(1)
	})

	const n = 25
	for i := 0; i < n; i++ {
		if ok := fooTop.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: i}}); !ok {
			t.Fatalf("foo Publish %d returned false", i)
		}
		if ok := barTop.Publish(pubsub.Event[barEvent]{Payload: barEvent{Name: "x"}}); !ok {
			t.Fatalf("bar Publish %d returned false", i)
		}
	}

	// PRIMARY: the panicking subscriber's drain loop survived each contained
	// panic and was re-entered for every event. This is the real per-subscriber
	// containment guarantee — it distinguishes "panic contained, drain loop
	// continues" from "panic escaped deliver, the loop-level recover swallowed
	// it once, subscriber permanently deaf".
	waitFor(t, func() bool { return panicSeen.Load() == n })

	// The healthy subscribers eventually see every event despite the
	// co-located panicker.
	waitFor(t, func() bool { return healthyFoo.Load() == n })
	waitFor(t, func() bool { return healthyBar.Load() == n })

	// SECONDARY: the panics were recovered and counted, not propagated.
	waitFor(t, func() bool { return fooTop.Stats().PanicsRecovered >= n })

	// Bus is still alive: a fresh publish is still accepted.
	if ok := fooTop.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: 999}}); !ok {
		t.Fatal("bus did not survive subscriber panic: Publish returned false")
	}
}

// (c) Drop-oldest under buffer pressure: DroppedTotal increments and the
// NEWEST events win. A blocked subscriber lets the buffer fill; once full,
// the oldest queued event is evicted in favor of each new one.
//
// MUST go RED if the bound is removed (unbounded buffer): nothing would be
// dropped, DroppedTotal stays 0, and the assertion on the newest-wins set
// would also fail because every event would be delivered.
func TestTopic_DropOldest(t *testing.T) {
	const buf = 4
	top := newTopic[fooEvent](t, pubsub.WithBuffer(buf))

	release := make(chan struct{})
	var mu sync.Mutex
	var delivered []int

	top.Subscribe(func(e pubsub.Event[fooEvent]) {
		// Block on the first event so the buffer backs up behind it.
		if e.Payload.N == 0 {
			<-release
		}
		mu.Lock()
		delivered = append(delivered, e.Payload.N)
		mu.Unlock()
	})

	// Event 0 enters the drain loop and blocks. The remaining publishes
	// queue into the bounded buffer; once it overflows, drop-oldest evicts.
	const total = 20
	for i := 0; i < total; i++ {
		top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: i}})
	}

	// Some events were dropped because the consumer was blocked.
	waitFor(t, func() bool { return top.Stats().DroppedTotal > 0 })

	close(release) // unblock the consumer; let the buffer drain.

	// After draining, total published == delivered + dropped (event 0 is
	// the one in-flight, the rest split between buffered-delivered and
	// dropped).
	waitFor(t, func() bool {
		s := top.Stats()
		mu.Lock()
		d := len(delivered)
		mu.Unlock()
		return int64(d)+s.DroppedTotal == int64(total)
	})

	// Newest-wins: the final event (total-1) is never dropped — it is the
	// most recent and drop-oldest evicts from the front.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range delivered {
			if n == total-1 {
				return true
			}
		}
		return false
	})
}

// (d) Publish returns false on a closed topic; true when accepted.
//
// The engine is drop-oldest (newest-wins): a full buffer NEVER makes Publish
// return false — enqueue always accepts and evicts the oldest, signaling
// back-pressure via DroppedTotal (proven by TestTopic_DropOldest), not a false
// return. So the only path where Publish returns false is a closed topic, which
// is what this test asserts.
func TestTopic_PublishBackpressureSignal(t *testing.T) {
	top := newTopic[fooEvent](t)
	top.Subscribe(func(e pubsub.Event[fooEvent]) {})
	if err := top.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if ok := top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: 1}}); ok {
		t.Fatal("Publish on a closed topic returned true; want false")
	}
}

// (e) Concurrent producers + consumers are race-clean under -race.
func TestTopic_ConcurrentRaceClean(t *testing.T) {
	top := newTopic[fooEvent](t, pubsub.WithBuffer(64))

	var received atomic.Int64
	const subs = 4
	for s := 0; s < subs; s++ {
		top.Subscribe(func(e pubsub.Event[fooEvent]) {
			received.Add(1)
		})
	}

	const producers = 8
	const perProducer = 200
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: base + i}})
			}
		}(p * perProducer)
	}
	wg.Wait()

	// Every accepted publish is eventually delivered or accounted as
	// dropped across all subscribers. We only assert race-cleanliness and
	// that the pipe kept running; exact counts depend on scheduling.
	s := top.Stats()
	if s.PublishedTotal == 0 {
		t.Fatal("expected some published events")
	}
	_ = received.Load()
}

// (g) SECURITY-CRITICAL regression: concurrent Publish + Close must never
// panic with "send on closed channel". A Publish that wins the race against
// Close holds the read lock across its whole fan-out; Close takes the write
// lock before closing any queue, so a closed queue is never sent to. A Publish
// that arrives after Close sees closed==true and returns false.
//
// This MUST go RED against a snapshot-then-release-then-enqueue Publish: that
// impl drops the lock before enqueuing, lets Close close the queue underneath
// an in-flight send, and panics — killing PID 1 and stranding eBPF.
func TestTopic_ConcurrentPublishCloseNoPanic(t *testing.T) {
	const iters = 2000
	const producers = 8

	for iter := 0; iter < iters; iter++ {
		// buffer=1 maximizes overlap of enqueue with close.
		top := newTopic[fooEvent](t, pubsub.WithBuffer(1))
		for s := 0; s < 3; s++ {
			top.Subscribe(func(e pubsub.Event[fooEvent]) {})
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(producers + 1)

		for p := 0; p < producers; p++ {
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 50; i++ {
					// The only legal outcomes are accepted (true) or
					// rejected-because-closed (false). A panic here is the bug.
					_ = top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: i}})
				}
			}()
		}

		go func() {
			defer wg.Done()
			<-start
			if err := top.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()

		close(start)
		wg.Wait()

		// Once closed, Publish is definitively false.
		if ok := top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: -1}}); ok {
			t.Fatalf("iter %d: Publish after Close returned true; want false", iter)
		}
	}
}

// (h) The orchestrator-injected publish hook fires exactly once per ACCEPTED
// Publish, BEFORE fan-out; it does NOT fire on a rejected (closed) Publish;
// passing nil clears it; and a panicking hook is contained so the bus and
// subscribers survive.
func TestTopic_Hook(t *testing.T) {
	t.Run("audit hook emits exactly one line per accepted publish", func(t *testing.T) {
		var buf bytes.Buffer
		top, err := pubsub.NewTopic[fooEvent](logger.NewWriter(&buf))
		if err != nil {
			t.Fatalf("NewTopic: %v", err)
		}
		t.Cleanup(func() { _ = top.Close() })

		const n = 50
		for i := 0; i < n; i++ {
			if ok := top.Publish(pubsub.Event[fooEvent]{Source: "producer", Payload: fooEvent{N: i}}); !ok {
				t.Fatalf("Publish %d returned false", i)
			}
		}

		// The hook runs synchronously in the producer goroutine before fan-out,
		// so all lines are written by the time the loop returns.
		got := countLines(buf.Bytes())
		if got != n {
			t.Fatalf("audit line count: got %d want %d", got, n)
		}
		if pub := top.Stats().PublishedTotal; pub != n {
			t.Fatalf("PublishedTotal: got %d want %d", pub, n)
		}
	})

	t.Run("does not fire on a closed (rejected) publish", func(t *testing.T) {
		var buf bytes.Buffer
		top, err := pubsub.NewTopic[fooEvent](logger.NewWriter(&buf))
		if err != nil {
			t.Fatalf("NewTopic: %v", err)
		}
		if err := top.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if ok := top.Publish(pubsub.Event[fooEvent]{Payload: fooEvent{N: 1}}); ok {
			t.Fatal("Publish on closed topic returned true")
		}
		if got := countLines(buf.Bytes()); got != 0 {
			t.Fatalf("audit hook fired on rejected publish: got %d lines want 0", got)
		}
	})

	t.Run("panicking payload marshaler is contained; bus and subscribers survive", func(t *testing.T) {
		// A real (enabled) logger is required: a Nop logger's disabled event
		// short-circuits EmbedObject without invoking MarshalZerologObject, so
		// the panic would never fire. Discard the output; we assert on counters.
		top, err := pubsub.NewTopic[panicMarshaler](logger.NewWriter(io.Discard))
		if err != nil {
			t.Fatalf("NewTopic: %v", err)
		}
		t.Cleanup(func() { _ = top.Close() })
		var delivered atomic.Int64
		top.Subscribe(func(e pubsub.Event[panicMarshaler]) { delivered.Add(1) })

		const n = 10
		for i := 0; i < n; i++ {
			// Publish must return normally despite the panicking marshaler in the
			// audit hook.
			if ok := top.Publish(pubsub.Event[panicMarshaler]{Payload: panicMarshaler{}}); !ok {
				t.Fatalf("Publish %d returned false through a panicking marshaler", i)
			}
		}

		// Subscribers still receive: the hook panic was contained, not fatal.
		waitFor(t, func() bool { return delivered.Load() == n })
		// Hook panics are counted separately from subscriber panics.
		waitFor(t, func() bool { return top.Stats().HookPanicsRecovered >= n })
		if got := top.Stats().PanicsRecovered; got != 0 {
			t.Fatalf("hook panic miscounted as subscriber panic: PanicsRecovered=%d", got)
		}
	})
}

// (f) Constructor returns an error (not a panic) on invalid options.
func TestNewTopic_InvalidOptions(t *testing.T) {
	if _, err := pubsub.NewTopic[fooEvent](logger.Nop(), pubsub.WithBuffer(0)); err == nil {
		t.Fatal("NewTopic with zero buffer: want error, got nil")
	}
	if _, err := pubsub.NewTopic[fooEvent](logger.Nop(), pubsub.WithBuffer(-1)); err == nil {
		t.Fatal("NewTopic with negative buffer: want error, got nil")
	}
	if _, err := pubsub.NewTopic[fooEvent](nil); err == nil {
		t.Fatal("NewTopic with nil logger: want error, got nil")
	}
}

// Stats reports the pipe counters and nothing domain-specific.
func TestTopic_StatsArePipeOnly(t *testing.T) {
	const buf = 8
	top := newTopic[fooEvent](t, pubsub.WithBuffer(buf))
	top.Subscribe(func(e pubsub.Event[fooEvent]) {})

	s := top.Stats()
	if s.Subscribers != 1 {
		t.Fatalf("Subscribers: got %d want 1", s.Subscribers)
	}
	if s.QueueCapacity != buf {
		t.Fatalf("QueueCapacity: got %d want %d", s.QueueCapacity, buf)
	}
	if s.PublishedTotal != 0 || s.DroppedTotal != 0 {
		t.Fatalf("fresh counters nonzero: %+v", s)
	}
}

// Publishing after Close yields ErrTopicClosed-classifiable behavior via the
// bool signal, and a second Close is idempotent (no panic).
func TestTopic_CloseIdempotent(t *testing.T) {
	top := newTopic[fooEvent](t)
	if err := top.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := top.Close(); err != nil {
		t.Fatalf("second Close should be idempotent: %v", err)
	}
}
