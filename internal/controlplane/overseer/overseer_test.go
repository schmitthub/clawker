package overseer_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestStats_TracksSubscribersAndCounts(t *testing.T) {
	o := newTestOverseer(t)
	sub1, _ := overseer.Subscribe[evContainerStarted](o, "s1")
	defer sub1.Unsubscribe()
	sub2, _ := overseer.Subscribe[evContainerRemoved](o, "s2")
	defer sub2.Unsubscribe()

	st := o.Stats()
	if st.Subscribers != 2 {
		t.Fatalf("Subscribers=%d, want 2", st.Subscribers)
	}

	overseer.Publish(o, evContainerStarted{ID: "c1", At: time.Now()})
	// Drain.
	<-sub1.C

	st = o.Stats()
	if st.PublishedTotal == 0 {
		t.Fatal("PublishedTotal should be > 0")
	}
	if st.ContainersKnown != 1 {
		t.Fatalf("ContainersKnown=%d, want 1", st.ContainersKnown)
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
