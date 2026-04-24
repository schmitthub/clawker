package informer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

// newTestInformer builds a started Informer with a deterministic
// clock. t.Cleanup closes it so tests don't leak goroutines.
func newTestInformer(t *testing.T) (*informer.Informer, *fakeClock) {
	t.Helper()
	clk := &fakeClock{at: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	inf := informer.New(informer.Options{
		WriteQueueSize:   32,
		SubscriberBuffer: 4,
		Now:              clk.Now,
	})
	if err := inf.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = inf.Close()
	})
	return inf, clk
}

type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func tx(source, verb string) informer.Transition {
	return informer.Transition{Source: source, Verb: verb}
}

func res(kind, id string, labels map[string]string) informer.Resource {
	return informer.Resource{
		Kind:      kind,
		ID:        id,
		Labels:    labels,
		Lifecycle: informer.LifecycleLive,
	}
}

func TestUpsert_AddsResourceWithFirstSeenAndHistory(t *testing.T) {
	inf, clk := newTestInformer(t)
	ctx := context.Background()

	if err := inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, ok := inf.Get(informer.Key{Kind: "container", ID: "c1"})
	if !ok {
		t.Fatal("Get returned ok=false after Upsert")
	}
	if got.FirstSeen != clk.Now() {
		t.Errorf("FirstSeen=%v want %v", got.FirstSeen, clk.Now())
	}
	if got.LastSeen != clk.Now() {
		t.Errorf("LastSeen=%v want %v", got.LastSeen, clk.Now())
	}
	if len(got.History) != 1 || got.History[0].Verb != "start" {
		t.Errorf("History=%+v want one entry verb=start", got.History)
	}
}

func TestUpsert_MergesLabelsAttrsAndAdvancesLastSeen(t *testing.T) {
	inf, clk := newTestInformer(t)
	ctx := context.Background()

	// Initial insert.
	if err := inf.Upsert(ctx,
		informer.Resource{
			Kind: "container", ID: "c1",
			Labels:    map[string]string{"a": "1"},
			Attrs:     map[string]string{"p": "x"},
			Lifecycle: informer.LifecycleLive,
		},
		tx("docker", "start"),
	); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	first := clk.Now()
	clk.advance(5 * time.Second)

	// Merge with additional label + overlapping label overwrite.
	if err := inf.Upsert(ctx,
		informer.Resource{
			Kind: "container", ID: "c1",
			Labels: map[string]string{"a": "2", "b": "3"},
		},
		tx("docker", "update"),
	); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, _ := inf.Get(informer.Key{Kind: "container", ID: "c1"})
	if got.Labels["a"] != "2" || got.Labels["b"] != "3" {
		t.Errorf("labels=%v want a=2 b=3", got.Labels)
	}
	if got.Attrs["p"] != "x" {
		t.Errorf("attrs=%v want p retained", got.Attrs)
	}
	if !got.FirstSeen.Equal(first) {
		t.Errorf("FirstSeen drifted: %v", got.FirstSeen)
	}
	if !got.LastSeen.After(first) {
		t.Errorf("LastSeen not advanced: %v", got.LastSeen)
	}
	if len(got.History) != 2 {
		t.Errorf("history=%d want 2", len(got.History))
	}
}

func TestRemove_SoftDeletesAndKeepsHistory(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()
	key := informer.Key{Kind: "container", ID: "c1"}

	_ = inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start"))
	if err := inf.Remove(ctx, key, tx("docker", "die")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got, ok := inf.Get(key)
	if !ok {
		t.Fatal("resource evicted on Remove; expected soft-delete")
	}
	if got.Lifecycle != informer.LifecycleGone {
		t.Errorf("Lifecycle=%q want %q", got.Lifecycle, informer.LifecycleGone)
	}
	if len(got.History) < 2 {
		t.Errorf("history lost: %+v", got.History)
	}
}

func TestRemove_IsIdempotent(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()
	key := informer.Key{Kind: "container", ID: "c1"}

	_ = inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start"))
	_, ch, cancel := inf.Subscribe(informer.Filter{})
	defer cancel()
	// Drain the DeltaAdded that was produced before Subscribe — Subscribe
	// returns current snapshot + forward channel, so no retroactive delta
	// should be in ch.

	_ = inf.Remove(ctx, key, tx("docker", "die"))
	select {
	case d := <-ch:
		if d.Kind != informer.DeltaRemoved {
			t.Fatalf("first delta kind=%v want removed", d.Kind)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected DeltaRemoved on first Remove")
	}

	_ = inf.Remove(ctx, key, tx("docker", "die-again"))
	select {
	case d := <-ch:
		t.Fatalf("unexpected delta on duplicate Remove: %+v", d)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPatch_AppliesFnAndReanchorsIdentity(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()
	key := informer.Key{Kind: "container", ID: "c1"}

	_ = inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start"))

	err := inf.Patch(ctx, key, func(r *informer.Resource) {
		// Attempt identity mutation — must be overridden.
		r.Kind = "HIJACKED"
		r.ID = "HIJACKED"
		r.Lifecycle = "draining"
	}, tx("cli", "drain"))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, ok := inf.Get(key)
	if !ok {
		t.Fatal("Get missed after Patch: identity re-anchor broken")
	}
	if got.Kind != "container" || got.ID != "c1" {
		t.Errorf("identity drifted: kind=%q id=%q", got.Kind, got.ID)
	}
	if got.Lifecycle != "draining" {
		t.Errorf("lifecycle=%q want draining", got.Lifecycle)
	}
}

func TestPatch_NoOpOnUnknownKey(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	err := inf.Patch(ctx, informer.Key{Kind: "container", ID: "nope"}, func(r *informer.Resource) {
		t.Fatal("fn should not be called for unknown key")
	}, tx("cli", "drain"))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
}

func TestLinkUnlinkRelation_DirectedAndReversible(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_ = inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("network", "n1", nil), tx("docker", "net-create"))

	cKey := informer.Key{Kind: "container", ID: "c1"}
	nKey := informer.Key{Kind: "network", ID: "n1"}

	err := inf.LinkRelation(ctx, informer.Relation{
		From: cKey, To: nKey, Kind: "attached-to",
	}, tx("docker", "connect"))
	if err != nil {
		t.Fatalf("LinkRelation: %v", err)
	}

	out := inf.Neighbors(cKey, "attached-to")
	if len(out) != 1 || out[0].Key() != nKey {
		t.Errorf("Neighbors=%+v want one pointing at %v", out, nKey)
	}
	in := inf.Incoming(nKey, "attached-to")
	if len(in) != 1 || in[0].Key() != cKey {
		t.Errorf("Incoming=%+v want one pointing at %v", in, cKey)
	}

	// Directed: no reverse edge.
	if rev := inf.Neighbors(nKey, "attached-to"); len(rev) != 0 {
		t.Errorf("reverse edge leaked: %+v", rev)
	}

	if err := inf.UnlinkRelation(ctx, cKey, nKey, "attached-to", tx("docker", "disconnect")); err != nil {
		t.Fatalf("UnlinkRelation: %v", err)
	}
	if out := inf.Neighbors(cKey, "attached-to"); len(out) != 0 {
		t.Errorf("edge survived Unlink: %+v", out)
	}
}

// Subscribe/cancel must not race with the writer's fan-out — a cancel
// that closes the delta channel while offer() is mid-send must be
// serialized by Informer.mu. Stress test under -race; pre-fix this
// would intermittently panic with "send on closed channel".
func TestSubscribe_CancelDuringFanoutDoesNotRace(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	// Hot writer: keep emitting deltas across the whole test.
	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = inf.Upsert(ctx, res("container", "c1", map[string]string{"i": "x"}), tx("docker", "tick"))
		}
	})

	// Churn subscribers: subscribe + cancel as fast as possible.
	var churn sync.WaitGroup
	for range 20 {
		churn.Go(func() {
			for range 50 {
				_, _, cancel := inf.Subscribe(informer.Filter{})
				cancel()
			}
		})
	}

	churn.Wait()
	close(stop)
	writers.Wait()
}

// Relation deltas carry nil Before/After. A non-empty resource filter
// must not panic while gating these deltas — the empty-filter check in
// subscriberFilterAllowsRelation is the only legitimate way through.
func TestSubscribe_RelationDeltaWithResourceFilterDoesNotPanic(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_, _, cancel := inf.Subscribe(informer.Filter{Kinds: []string{"container"}})
	defer cancel()

	cKey := informer.Key{Kind: "container", ID: "c1"}
	nKey := informer.Key{Kind: "network", ID: "n1"}

	// Pre-fix this would panic the writer goroutine on Filter.matches(nil).
	if err := inf.LinkRelation(ctx, informer.Relation{
		From: cKey, To: nKey, Kind: "attached-to",
	}, tx("docker", "connect")); err != nil {
		t.Fatalf("LinkRelation: %v", err)
	}
	if err := inf.UnlinkRelation(ctx, cKey, nKey, "attached-to", tx("docker", "disconnect")); err != nil {
		t.Fatalf("UnlinkRelation: %v", err)
	}

	// Writer still alive — a follow-up write must succeed.
	if err := inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start")); err != nil {
		t.Fatalf("Upsert after relation deltas: %v", err)
	}
}

func TestFilter_KindsAndLabelsNarrow(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_ = inf.Upsert(ctx, res("container", "c1", map[string]string{"env": "prod"}), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("container", "c2", map[string]string{"env": "dev"}), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("network", "n1", map[string]string{"env": "prod"}), tx("docker", "net-create"))

	// Kind filter.
	got := inf.List(informer.Filter{Kinds: []string{"container"}})
	if len(got) != 2 {
		t.Errorf("Kinds filter got %d want 2", len(got))
	}

	// Label equals filter.
	got = inf.List(informer.Filter{
		Labels: informer.LabelSelector{Equals: map[string]string{"env": "prod"}},
	})
	if len(got) != 2 {
		t.Errorf("Labels filter got %d want 2 (c1 + n1)", len(got))
	}

	// Combined.
	got = inf.List(informer.Filter{
		Kinds:  []string{"container"},
		Labels: informer.LabelSelector{Equals: map[string]string{"env": "prod"}},
	})
	if len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("combined filter got %+v want [c1]", got)
	}
}

func TestSubscribe_SnapshotPlusForwardDeltas(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_ = inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("container", "c2", nil), tx("docker", "start"))

	snap, ch, cancel := inf.Subscribe(informer.Filter{Kinds: []string{"container"}})
	defer cancel()
	if len(snap) != 2 {
		t.Fatalf("snapshot len=%d want 2", len(snap))
	}

	_ = inf.Upsert(ctx, res("container", "c3", nil), tx("docker", "start"))
	select {
	case d := <-ch:
		if d.Kind != informer.DeltaAdded || d.After == nil || d.After.ID != "c3" {
			t.Errorf("unexpected delta: %+v", d)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscriber did not receive c3 delta")
	}
}

func TestSubscribe_DropOldestOnFullBuffer(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_, ch, cancel := inf.Subscribe(informer.Filter{})
	defer cancel()

	// SubscriberBuffer is 4 (set by newTestInformer). Push many without draining.
	for n := range 20 {
		id := string(rune('a' + n))
		_ = inf.Upsert(ctx, res("container", id, nil), tx("docker", "start"))
	}

	// Let the writer catch up.
	time.Sleep(50 * time.Millisecond)

	// Channel should have at most SubscriberBuffer (4) entries.
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			goto done
		}
	}
done:
	if n > 4 {
		t.Errorf("got %d items, want <= 4 (buffer size)", n)
	}

	st := inf.Stats()
	if st.DeltasDroppedTotal == 0 {
		t.Errorf("DeltasDroppedTotal=%d want > 0", st.DeltasDroppedTotal)
	}
}

func TestHistoryRing_BoundedAt50(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	for n := range 70 {
		_ = inf.Upsert(ctx, res("container", "c1", nil), informer.Transition{
			Source: "docker", Verb: "heartbeat",
			Attrs: map[string]string{"n": string(rune('A' + n%26))},
		})
	}

	got := inf.History(informer.Key{Kind: "container", ID: "c1"})
	if len(got) != 50 {
		t.Errorf("history len=%d want 50", len(got))
	}
}

func TestClosed_WritesReturnErrClosed(t *testing.T) {
	inf := informer.New(informer.Options{})
	_ = inf.Start(context.Background())
	_ = inf.Close()

	err := inf.Upsert(context.Background(), res("container", "c1", nil), tx("docker", "start"))
	if err == nil {
		t.Fatal("Upsert on closed informer returned nil error")
	}
}

func TestValidateKey_EmptyRejected(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	if err := inf.Upsert(ctx, informer.Resource{Kind: "", ID: "c1"}, tx("docker", "start")); err == nil {
		t.Error("empty Kind accepted")
	}
	if err := inf.Upsert(ctx, informer.Resource{Kind: "container", ID: ""}, tx("docker", "start")); err == nil {
		t.Error("empty ID accepted")
	}
}

func TestCompositeKey_SameIDDifferentKindsCoexist(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_ = inf.Upsert(ctx, res("container", "x", nil), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("network", "x", nil), tx("docker", "net-create"))

	if _, ok := inf.Get(informer.Key{Kind: "container", ID: "x"}); !ok {
		t.Error("container x missing")
	}
	if _, ok := inf.Get(informer.Key{Kind: "network", ID: "x"}); !ok {
		t.Error("network x missing")
	}
	if st := inf.Stats(); st.Resources != 2 {
		t.Errorf("Stats.Resources=%d want 2", st.Resources)
	}
}
