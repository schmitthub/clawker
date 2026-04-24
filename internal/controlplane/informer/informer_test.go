package informer_test

import (
	"context"
	"errors"
	"fmt"
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

func res(kind, id string, labels map[string]string) informer.ResourceUpdate {
	return informer.ResourceUpdate{
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
		informer.ResourceUpdate{
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
		informer.ResourceUpdate{
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
	})
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

	if err := inf.UnlinkRelation(ctx, cKey, nKey, "attached-to"); err != nil {
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
	}); err != nil {
		t.Fatalf("LinkRelation: %v", err)
	}
	if err := inf.UnlinkRelation(ctx, cKey, nKey, "attached-to"); err != nil {
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

	// SubscriberBuffer is 4 (set by newTestInformer). Push many without
	// draining. Use 2-char IDs so the later ones sort strictly after the
	// earlier ones — "id-00", "id-01", ..., "id-19".
	const total = 20
	for n := range total {
		id := fmt.Sprintf("id-%02d", n)
		_ = inf.Upsert(ctx, res("container", id, nil), tx("docker", "start"))
	}

	// Synchronize on the writer via Stats rather than time.Sleep:
	// once WritesTotal reaches `total`, every Upsert's apply() has
	// returned, so no additional drops can land.
	deadline := time.Now().Add(2 * time.Second)
	for inf.Stats().WritesTotal < uint64(total) {
		if time.Now().After(deadline) {
			t.Fatalf("writer never caught up: stats=%+v", inf.Stats())
		}
		time.Sleep(time.Millisecond)
	}

	// Drain everything currently buffered.
	var got []string
	for {
		select {
		case d, ok := <-ch:
			if !ok {
				goto done
			}
			if d.After == nil {
				t.Fatalf("unexpected delta shape: %+v", d)
			}
			got = append(got, d.After.ID)
			continue
		default:
		}
		break
	}
done:
	// At most SubscriberBuffer entries survived.
	if len(got) > 4 {
		t.Errorf("buffered %d > buffer capacity 4; got=%v", len(got), got)
	}

	// Drop-oldest ordering: everything in `got` must be strictly
	// monotonically increasing IDs, and the smallest surviving ID
	// must be > id-00 — if the oldest was preserved instead of
	// dropped, id-00 would still be here and id-19 would be missing.
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Errorf("out-of-order buffered items: %v", got)
			break
		}
	}
	if len(got) > 0 && got[0] == "id-00" {
		t.Errorf("oldest (id-00) retained — drop-oldest broken; got=%v", got)
	}
	last := fmt.Sprintf("id-%02d", total-1)
	if len(got) > 0 && got[len(got)-1] != last {
		t.Errorf("newest (%s) not retained; got=%v", last, got)
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
	if !errors.Is(err, informer.ErrClosed) {
		t.Fatalf("Upsert on closed informer err=%v want ErrClosed", err)
	}
}

// ErrNotStarted guards writers from submitting against an informer
// whose writer goroutine has never been launched. Without the check,
// submit would block on the queue send forever (queue is never
// drained without Start) and waitOp would hang until the caller's
// ctx cancels — silent bug class.
func TestWriteBeforeStart_ReturnsErrNotStarted(t *testing.T) {
	inf := informer.New(informer.Options{})
	defer func() { _ = inf.Close() }()

	err := inf.Upsert(context.Background(), res("container", "c1", nil), tx("docker", "start"))
	if !errors.Is(err, informer.ErrNotStarted) {
		t.Fatalf("Upsert before Start err=%v want ErrNotStarted", err)
	}
}

// When the Start context cancels, the informer converges on the same
// shutdown state as Close: subsequent writes see ErrClosed (not a
// deadlock waiting for a writer that has already exited). Pre-fix,
// writerLoop exited on ctx.Done but left closed=false and the queue
// open, so feeders would enqueue into a queue with no drainer.
func TestStartCtxCancel_TransitionsToClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inf := informer.New(informer.Options{})
	if err := inf.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = inf.Close() })

	// Baseline: one successful write proves the writer is live.
	if err := inf.Upsert(ctx, res("container", "c1", nil), tx("docker", "start")); err != nil {
		t.Fatalf("pre-cancel Upsert: %v", err)
	}

	cancel()

	// Writer observes ctx.Done, triggers shutdown, exits. Poll until
	// writes start returning ErrClosed — the transition is
	// asynchronous.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := inf.Upsert(context.Background(), res("container", "c2", nil), tx("docker", "start"))
		if errors.Is(err, informer.ErrClosed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer never transitioned to closed after ctx cancel: last err=%v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

// If the caller's ctx cancels while the write queue is full, submit
// must return ctx.Err() rather than blocking forever. Backpressure
// is the documented behavior; a broken submit that swallowed ctx
// would silently hang feeders.
func TestUpsert_CtxCancelWhileQueueFull_ReturnsCtxErr(t *testing.T) {
	// Stall the writer goroutine via a Now callback that blocks until
	// we release it. apply() calls Now() on every op, so the first
	// op parks the writer forever-until-release and the queue fills
	// up behind it. With WriteQueueSize=1, item #0 is stuck inside
	// apply() → item #1 sits in the queue buffer → item #2 blocks on
	// the send. Canceling item #2's ctx must return context.Canceled.
	release := make(chan struct{})
	nowBlocked := make(chan struct{}, 1)
	var once sync.Once
	now := func() time.Time {
		once.Do(func() {
			nowBlocked <- struct{}{}
			<-release
		})
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	inf := informer.New(informer.Options{WriteQueueSize: 1, Now: now})
	if err := inf.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		// Let the stalled apply() complete so Close can drain.
		select {
		case <-release:
		default:
			close(release)
		}
		_ = inf.Close()
	})

	// First Upsert: the writer goroutine picks this up and parks
	// inside apply() on the blocked Now.
	seedDone := make(chan error, 1)
	go func() {
		seedDone <- inf.Upsert(context.Background(), res("container", "c0", nil), tx("docker", "start"))
	}()

	// Wait until the writer is definitely stalled inside apply().
	select {
	case <-nowBlocked:
	case <-time.After(2 * time.Second):
		t.Fatal("writer never entered apply()")
	}

	// Second Upsert: fills the queue's 1-slot buffer.
	fillerDone := make(chan error, 1)
	go func() {
		fillerDone <- inf.Upsert(context.Background(), res("container", "c1", nil), tx("docker", "start"))
	}()
	// Give it time to land in the buffer.
	time.Sleep(20 * time.Millisecond)

	// Third Upsert on a cancelable ctx: must block on the send.
	cancelCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- inf.Upsert(cancelCtx, res("container", "c2", nil), tx("docker", "start"))
	}()
	// Give the goroutine time to reach the blocked send.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Upsert err=%v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upsert did not unblock after ctx cancel — submit is not honoring ctx")
	}

	// Unblock the writer so seed + filler can complete; t.Cleanup
	// runs Close afterwards.
	close(release)
	<-seedDone
	<-fillerDone
}

// Close drains pending ops: a write submitted just before Close must
// return a commit outcome (either ErrClosed because its send lost
// the race, or success because it was applied during drain).
// Invariant: submit never leaves waitOp blocked forever.
func TestClose_DrainsPendingOrReturnsErrClosed(t *testing.T) {
	inf := informer.New(informer.Options{WriteQueueSize: 64})
	if err := inf.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const writers = 50
	results := make(chan error, writers)
	for n := range writers {
		go func(i int) {
			id := fmt.Sprintf("c-%03d", i)
			results <- inf.Upsert(context.Background(), res("container", id, nil), tx("docker", "start"))
		}(n)
	}
	// Close races the writers; every result must be either nil or
	// ErrClosed, never a hang or a different error.
	_ = inf.Close()
	for range writers {
		select {
		case err := <-results:
			if err != nil && !errors.Is(err, informer.ErrClosed) {
				t.Errorf("unexpected write err during Close race: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("write did not return after Close — submit/waitOp blocked forever")
		}
	}
}

// Close must be idempotent — the second call observes closed=true
// and returns without touching the subscriber set or the writer.
func TestClose_Idempotent(t *testing.T) {
	inf := informer.New(informer.Options{})
	_ = inf.Start(context.Background())
	if err := inf.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := inf.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Subscriber channels must close on Close so consumers using
// `for d := range ch` exit. Pre-fix, Close closed channels only via
// closeAll; the writer-exit path (ctx.Done) left them open.
func TestClose_ClosesSubscriberChannels(t *testing.T) {
	inf, _ := newTestInformer(t)
	_, ch, _ := inf.Subscribe(informer.Filter{})

	// Close the informer; the subscriber channel must close.
	// newTestInformer's t.Cleanup also calls Close, which is fine —
	// second call is a no-op.
	_ = inf.Close()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscriber channel delivered a value after Close; expected close")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscriber channel not closed after Close")
	}
}

// Upsert merge semantics: an empty string in Labels/Attrs overwrites
// the stored value (documented contract). Feeders use this to signal
// "clear this field" without discarding the rest of the map. Patch
// is the documented way to clear a whole map.
func TestUpsert_EmptyStringClearsValueViaMerge(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	_ = inf.Upsert(ctx,
		informer.ResourceUpdate{
			Kind: "container", ID: "c1",
			Labels:    map[string]string{"a": "1", "b": "2"},
			Lifecycle: informer.LifecycleLive,
		},
		tx("docker", "start"),
	)
	if err := inf.Upsert(ctx,
		informer.ResourceUpdate{
			Kind: "container", ID: "c1",
			Labels: map[string]string{"a": ""},
		},
		tx("docker", "update"),
	); err != nil {
		t.Fatalf("Upsert merge: %v", err)
	}

	got, _ := inf.Get(informer.Key{Kind: "container", ID: "c1"})
	if v, ok := got.Labels["a"]; !ok || v != "" {
		t.Errorf("Labels[a]=%q,ok=%v want empty-string cleared (present with \"\")", v, ok)
	}
	if got.Labels["b"] != "2" {
		t.Errorf("Labels[b]=%q want 2 (untouched)", got.Labels["b"])
	}
}

// Subscribe deep-copies the caller's filter. A consumer that mutates
// its local filter after Subscribe must not see the mutation reflected
// in delivery. Pre-fix, Subscribe held the same map reference the
// caller owned, so a concurrent map write would race matches().
func TestSubscribe_FilterMutationAfterSubscribeHasNoEffect(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	filter := informer.Filter{
		Kinds: []string{"container"},
		Labels: informer.LabelSelector{
			Equals: map[string]string{"env": "prod"},
		},
	}
	_, ch, cancel := inf.Subscribe(filter)
	defer cancel()

	// Corrupt the caller's local filter. If Subscribe kept a shared
	// reference, `network` deltas would leak through.
	filter.Kinds = append(filter.Kinds, "network")
	filter.Labels.Equals["env"] = "dev"

	// Emit something that matches the ORIGINAL filter and something
	// that would only match the MUTATED filter.
	_ = inf.Upsert(ctx, res("container", "c1", map[string]string{"env": "prod"}), tx("docker", "start"))
	_ = inf.Upsert(ctx, res("network", "n1", map[string]string{"env": "dev"}), tx("docker", "net-create"))

	seen := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case d := <-ch:
			seen++
			if d.After == nil || d.After.Kind != "container" {
				t.Errorf("leaked delta for kind=%q — filter copy is broken", d.After.Kind)
			}
		case <-timeout:
			break loop
		}
	}
	if seen != 1 {
		t.Errorf("saw %d deltas want 1 (only the original filter should match)", seen)
	}
}

func TestValidateKey_EmptyRejected(t *testing.T) {
	inf, _ := newTestInformer(t)
	ctx := context.Background()

	if err := inf.Upsert(ctx, informer.ResourceUpdate{Kind: "", ID: "c1"}, tx("docker", "start")); err == nil {
		t.Error("empty Kind accepted")
	}
	if err := inf.Upsert(ctx, informer.ResourceUpdate{Kind: "container", ID: ""}, tx("docker", "start")); err == nil {
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
