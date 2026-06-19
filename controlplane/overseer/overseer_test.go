package overseer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// --- test event types --------------------------------------------------
//
// Defined here so the bus tests don't depend on the producer packages
// (dockerevents, agentdial). evApplyContainer mutates State.Containers
// to exercise the applier hook end-to-end.

type evContainerStarted struct {
	ID   string
	Name string
	At   time.Time
}

func (e evContainerStarted) EventName() string     { return "test.container.started" }
func (e evContainerStarted) OccurredAt() time.Time { return e.At }
func (e evContainerStarted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ID).Str("name", e.Name)
}
func (e evContainerStarted) ApplyTo(s *overseer.State) {
	s.Containers[e.ID] = overseer.ContainerView{
		ID:        e.ID,
		Name:      e.Name,
		Status:    overseer.ContainerStatusRunning,
		UpdatedAt: e.At,
	}
}

type evContainerRemoved struct {
	ID string
	At time.Time
}

func (e evContainerRemoved) EventName() string     { return "test.container.removed" }
func (e evContainerRemoved) OccurredAt() time.Time { return e.At }
func (e evContainerRemoved) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ID)
}
func (e evContainerRemoved) ApplyTo(s *overseer.State) {
	delete(s.Containers, e.ID)
}

// evNoApply has no ApplyTo — exercises the pure-pubsub path (event has
// no state side-effect, just notification).
type evNoApply struct {
	Note string
	At   time.Time
}

func (e evNoApply) EventName() string     { return "test.no_apply" }
func (e evNoApply) OccurredAt() time.Time { return e.At }
func (e evNoApply) MarshalZerologObject(z *zerolog.Event) {
	z.Str("note", e.Note)
}

// --- helpers ----------------------------------------------------------

func newTestOverseer(t *testing.T) *overseer.Overseer {
	t.Helper()
	o := overseer.New(overseer.Options{
		Logger:            logger.Nop(),
		PublishBufferSize: 64,
		SubscriberBuffer:  4,
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func recvWithin(t *testing.T, ch <-chan evContainerStarted, d time.Duration) (evContainerStarted, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(d):
		return evContainerStarted{}, false
	}
}

// --- tests ------------------------------------------------------------

func TestPublishSubscribe_DeliversTypedEvent(t *testing.T) {
	o := newTestOverseer(t)
	sub, ok := overseer.Subscribe[evContainerStarted](o, "")
	if !ok {
		t.Fatal("subscribe returned false")
	}
	defer sub.Unsubscribe()

	if !overseer.Publish(o, evContainerStarted{ID: "c1", Name: "alpha", At: time.Now()}) {
		t.Fatal("publish returned false")
	}

	ev, ok := recvWithin(t, sub.C, time.Second)
	if !ok {
		t.Fatal("did not receive event within 1s")
	}
	if ev.ID != "c1" {
		t.Fatalf("got id=%q, want c1", ev.ID)
	}
}

func TestSubscribe_FiltersByType(t *testing.T) {
	o := newTestOverseer(t)
	sub, ok := overseer.Subscribe[evContainerStarted](o, "")
	if !ok {
		t.Fatal("subscribe returned false")
	}
	defer sub.Unsubscribe()

	// Publish a different concrete type — must not appear on the
	// evContainerStarted subscriber's channel.
	overseer.Publish(o, evNoApply{Note: "ignore me", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "c1", At: time.Now()})

	ev, ok := recvWithin(t, sub.C, time.Second)
	if !ok {
		t.Fatal("did not receive event")
	}
	if ev.ID != "c1" {
		t.Fatalf("got id=%q, want c1 — type filter leaked", ev.ID)
	}

	// No second event should arrive (evNoApply was a different type).
	if _, more := recvWithin(t, sub.C, 100*time.Millisecond); more {
		t.Fatal("received unexpected second event — cross-type leak")
	}
}

func TestSubscribeFiltered_AppliesPredicate(t *testing.T) {
	o := newTestOverseer(t)
	sub, ok := overseer.SubscribeFiltered(o, "test", func(e evContainerStarted) bool {
		return e.Name == "match"
	})
	if !ok {
		t.Fatal("subscribe returned false")
	}
	defer sub.Unsubscribe()

	overseer.Publish(o, evContainerStarted{ID: "c1", Name: "skip", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "c2", Name: "match", At: time.Now()})

	ev, ok := recvWithin(t, sub.C, time.Second)
	if !ok {
		t.Fatal("did not receive event")
	}
	if ev.Name != "match" {
		t.Fatalf("filter leaked: got name=%q", ev.Name)
	}
}

func TestSnapshot_ReflectsApplyToHooks(t *testing.T) {
	o := newTestOverseer(t)

	t0 := time.Now()
	overseer.Publish(o, evContainerStarted{ID: "c1", Name: "alpha", At: t0})
	overseer.Publish(o, evContainerStarted{ID: "c2", Name: "bravo", At: t0.Add(time.Millisecond)})

	// Snapshot strips deep — drain by sending a sentinel and waiting.
	// Loop drains in order, so once we can read snapshot the prior
	// publishes are applied.
	state, ok := o.Snapshot(context.Background())
	if !ok {
		t.Fatal("snapshot returned false")
	}

	// Allow up to 50ms of slack for the run-loop to apply pending events
	// — Publish is fire-and-forget, Snapshot serializes after queued
	// publishes, so this should be near-instant in practice.
	deadline := time.Now().Add(50 * time.Millisecond)
	for len(state.Containers) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		state, _ = o.Snapshot(context.Background())
	}
	if len(state.Containers) != 2 {
		t.Fatalf("snapshot containers=%d, want 2", len(state.Containers))
	}
	if state.Containers["c1"].Status != overseer.ContainerStatusRunning {
		t.Fatalf("c1 status=%q, want running", state.Containers["c1"].Status)
	}

	// Publish a removal — state should reflect it after.
	overseer.Publish(o, evContainerRemoved{ID: "c1", At: t0.Add(2 * time.Millisecond)})

	deadline = time.Now().Add(50 * time.Millisecond)
	for len(state.Containers) > 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		state, _ = o.Snapshot(context.Background())
	}
	if _, present := state.Containers["c1"]; present {
		t.Fatal("c1 still present after Removed event")
	}
}

func TestSnapshot_DeepCopy(t *testing.T) {
	o := newTestOverseer(t)
	overseer.Publish(o, evContainerStarted{ID: "c1", Name: "alpha", At: time.Now()})

	// Wait for apply.
	deadline := time.Now().Add(100 * time.Millisecond)
	var state overseer.State
	for time.Now().Before(deadline) {
		state, _ = o.Snapshot(context.Background())
		if len(state.Containers) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(state.Containers) == 0 {
		t.Fatal("event never applied")
	}

	// Mutating the returned snapshot must not affect subsequent reads.
	delete(state.Containers, "c1")

	state2, _ := o.Snapshot(context.Background())
	if _, present := state2.Containers["c1"]; !present {
		t.Fatal("Snapshot leaked internal map — mutation visible across reads")
	}
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	o := newTestOverseer(t)
	sub, ok := overseer.Subscribe[evContainerStarted](o, "")
	if !ok {
		t.Fatal("subscribe returned false")
	}

	overseer.Publish(o, evContainerStarted{ID: "c1", At: time.Now()})
	if _, got := recvWithin(t, sub.C, time.Second); !got {
		t.Fatal("did not receive first event")
	}

	sub.Unsubscribe()

	// After Unsubscribe, the channel must close (drained range exits).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, ok := <-sub.C
		if !ok {
			return
		}
	}
	t.Fatal("channel did not close after Unsubscribe")
}

func TestClose_ClosesSubscriberChannels(t *testing.T) {
	o := overseer.New(overseer.Options{Logger: logger.Nop()})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	sub, _ := overseer.Subscribe[evContainerStarted](o, "")

	if err := o.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, ok := <-sub.C
		if !ok {
			return
		}
	}
	t.Fatal("subscriber channel did not close after bus Close")
}

func TestPublish_DropsOnFullBuffer(t *testing.T) {
	// Tiny subscriber buffer; one slow consumer should drop with the
	// counter incrementing while the bus stays alive.
	o := overseer.New(overseer.Options{
		Logger:            logger.Nop(),
		PublishBufferSize: 8,
		SubscriberBuffer:  1, // single-event buffer
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer o.Close()

	sub, _ := overseer.Subscribe[evContainerStarted](o, "")
	defer sub.Unsubscribe()

	// Don't drain. Publish many — most should drop.
	for range 50 {
		overseer.Publish(o, evContainerStarted{ID: "c", At: time.Now()})
	}

	// Give loop time to drain.
	time.Sleep(50 * time.Millisecond)

	st := o.Stats()
	if st.DroppedTotal == 0 {
		t.Fatal("expected DroppedTotal > 0 with full subscriber buffer")
	}
}

func TestRun_PanicInOneSubscriberDoesNotKillBus(t *testing.T) {
	// The bus does NOT recover from panics inside subscriber type
	// conversion (there's no user code on the loop's path). Subscriber
	// match predicates run on the loop — so a panicking predicate
	// could kill the loop. This test asserts the predicate path is
	// guarded.
	o := newTestOverseer(t)

	// Filter that panics on a specific event — but the bus dispatch
	// must continue serving other events to other subscribers.
	good, _ := overseer.Subscribe[evContainerStarted](o, "good")
	defer good.Unsubscribe()

	bad, _ := overseer.SubscribeFiltered(o, "panicker", func(e evContainerStarted) bool {
		if e.ID == "boom" {
			panic("filter panic")
		}
		return true
	})
	defer bad.Unsubscribe()

	overseer.Publish(o, evContainerStarted{ID: "boom", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "ok", At: time.Now()})

	// The good subscriber should still receive the "ok" event despite
	// the bad subscriber's filter panicking on "boom".
	deadline := time.Now().Add(time.Second)
	gotOK := false
	for time.Now().Before(deadline) && !gotOK {
		select {
		case ev := <-good.C:
			if ev.ID == "ok" {
				gotOK = true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !gotOK {
		t.Fatal("good subscriber starved — panic in another subscriber's filter killed dispatch")
	}
}

func TestPublish_BeforeStart_ReturnsFalse(t *testing.T) {
	o := overseer.New(overseer.Options{Logger: logger.Nop()})
	if overseer.Publish(o, evContainerStarted{ID: "c1", At: time.Now()}) {
		t.Fatal("Publish before Start should return false")
	}
}

func TestSubscribe_BeforeStart_ReturnsFalse(t *testing.T) {
	o := overseer.New(overseer.Options{Logger: logger.Nop()})
	if _, ok := overseer.Subscribe[evContainerStarted](o, ""); ok {
		t.Fatal("Subscribe before Start should return false")
	}
}

func TestSnapshot_AfterClose_ReturnsFalse(t *testing.T) {
	o := overseer.New(overseer.Options{Logger: logger.Nop()})
	_ = o.Start(context.Background())
	_ = o.Close()
	if _, ok := o.Snapshot(context.Background()); ok {
		t.Fatal("Snapshot after Close should return false")
	}
}

func TestConcurrent_PublishAndSubscribe_NoRace(t *testing.T) {
	// Stress: many producers, many consumers, run for a beat. Run
	// under -race to surface mutation hazards.
	o := newTestOverseer(t)

	var (
		producers = 8
		events    = 200
		consumers = 4
	)

	var received atomic.Int64

	subs := make([]overseer.Subscription[evContainerStarted], 0, consumers)
	var consumerWG sync.WaitGroup
	consumerDone := make(chan struct{})
	for i := range consumers {
		sub, ok := overseer.Subscribe[evContainerStarted](o, "")
		if !ok {
			t.Fatalf("subscribe %d failed", i)
		}
		subs = append(subs, sub)
		consumerWG.Add(1)
		go func(s overseer.Subscription[evContainerStarted]) {
			defer consumerWG.Done()
			for {
				select {
				case _, ok := <-s.C:
					if !ok {
						return
					}
					received.Add(1)
				case <-consumerDone:
					return
				}
			}
		}(sub)
	}

	var producerWG sync.WaitGroup
	for range producers {
		producerWG.Add(1)
		go func() {
			defer producerWG.Done()
			for range events {
				overseer.Publish(o, evContainerStarted{ID: "c", At: time.Now()})
			}
		}()
	}
	producerWG.Wait()

	// Allow tail drain.
	time.Sleep(100 * time.Millisecond)
	close(consumerDone)
	for _, s := range subs {
		s.Unsubscribe()
	}
	consumerWG.Wait()

	// Don't assert exact count — drop-oldest can shed events under
	// pressure. Just assert progress was made.
	if received.Load() == 0 {
		t.Fatal("no events received across all consumers")
	}
}

func TestPublishHook_FiresOncePerEvent(t *testing.T) {
	var hookCount atomic.Int64
	var seen []string
	var mu sync.Mutex
	o := overseer.New(overseer.Options{
		Logger:            logger.Nop(),
		PublishBufferSize: 16,
		SubscriberBuffer:  4,
		PublishHook: func(ev overseer.Event) {
			hookCount.Add(1)
			mu.Lock()
			seen = append(seen, ev.EventName())
			mu.Unlock()
		},
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })

	overseer.Publish(o, evContainerStarted{ID: "a", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "b", At: time.Now()})
	overseer.Publish(o, evContainerRemoved{ID: "a", At: time.Now()})
	overseer.Publish(o, evNoApply{Note: "ping", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "c", At: time.Now()})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && hookCount.Load() < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := hookCount.Load(); got != 5 {
		t.Fatalf("hook called %d times, want 5", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"test.container.started", "test.container.started", "test.container.removed", "test.no_apply", "test.container.started"}
	if len(seen) != len(want) {
		t.Fatalf("seen %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("event %d = %q, want %q (full: %v)", i, seen[i], want[i], seen)
		}
	}
}

func TestPublishHook_PanicRecovered(t *testing.T) {
	var afterPanic atomic.Int64
	o := overseer.New(overseer.Options{
		Logger:            logger.Nop(),
		PublishBufferSize: 16,
		SubscriberBuffer:  4,
		PublishHook: func(ev overseer.Event) {
			if e, ok := ev.(evContainerStarted); ok && e.ID == "boom" {
				panic("hook boom")
			}
			afterPanic.Add(1)
		},
	})
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })

	// Subscribe to confirm the bus loop is still alive after the panic.
	sub, ok := overseer.Subscribe[evContainerStarted](o, "after-boom")
	if !ok {
		t.Fatal("subscribe failed")
	}
	defer sub.Unsubscribe()

	overseer.Publish(o, evContainerStarted{ID: "boom", At: time.Now()})
	overseer.Publish(o, evContainerStarted{ID: "ok", At: time.Now()})

	if ev, ok := recvWithin(t, sub.C, time.Second); !ok || ev.ID != "boom" {
		t.Fatalf("expected first delivery to be boom (panic must NOT block dispatch), got %q ok=%v", ev.ID, ok)
	}
	if ev, ok := recvWithin(t, sub.C, time.Second); !ok || ev.ID != "ok" {
		t.Fatalf("subsequent event lost after hook panic: got %q ok=%v", ev.ID, ok)
	}
	if got := afterPanic.Load(); got != 1 {
		t.Fatalf("hook ran %d times after the panicking call, want 1", got)
	}
}

func TestNewLoggerHook_EmbedsTypePayload(t *testing.T) {
	// Captures the JSON line emitted by NewLoggerHook for an event whose
	// MarshalZerologObject populates type-specific fields. Asserts the
	// fields land at the top level (EmbedObject, not Object) alongside
	// the canonical event/occurred_at — guards against regressing back
	// to the old payload-stripped log shape that hid agent identity.
	var buf bytes.Buffer
	log := logger.NewWriter(&buf)
	hook := overseer.NewLoggerHook(log)
	hook(evContainerStarted{ID: "abc123", Name: "agent-7", At: time.Unix(1700000000, 0).UTC()})

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("hook output not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if got["event"] != "test.container.started" {
		t.Fatalf("event=%v, want test.container.started", got["event"])
	}
	if got["container_id"] != "abc123" {
		t.Fatalf("container_id=%v, want abc123 (EmbedObject did not flatten payload)", got["container_id"])
	}
	if got["name"] != "agent-7" {
		t.Fatalf("name=%v, want agent-7", got["name"])
	}
	if _, ok := got["occurred_at"]; !ok {
		t.Fatalf("occurred_at missing from log line: %v", got)
	}
	// Message text must be the event name — log-stream views are
	// otherwise indistinguishable when every entry says the same string.
	if got["message"] != "test.container.started" {
		t.Fatalf("message=%v, want test.container.started (line-view scannability regression)", got["message"])
	}
}
