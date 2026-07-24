package firewall

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// awaitTimeout bounds every test-side wait so a broken queue
// implementation fails loudly rather than hanging `go test`.
const awaitTimeout = 2 * time.Second

func recvOrFail(t *testing.T, ch <-chan ActionResult) ActionResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(awaitTimeout):
		t.Fatal("timed out waiting for ActionResult")
		return ActionResult{}
	}
}

// gate blocks a closure until release is called, then returns the
// configured result. Used to hold the worker mid-action while the test
// pushes more items into the queue behind it.
type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate {
	return &gate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gate) fn(value any, err error) ActionFunc {
	return func(ctx context.Context) (any, error) {
		close(g.entered)
		select {
		case <-g.release:
			return value, err
		case <-time.After(awaitTimeout):
			return nil, errors.New("gate: release timed out")
		}
	}
}

func (g *gate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(awaitTimeout):
		t.Fatal("gate never entered")
	}
}

func (g *gate) open() { close(g.release) }

func TestActionQueue_FIFOOrdering(t *testing.T) {
	q := NewActionQueue(nil)
	defer func() { _ = q.Close() }()

	// Hold the worker so every submission is queued before any runs —
	// that way the observed execution order is purely FIFO, not
	// dependent on scheduler timing.
	head := newGate()
	headReply := q.Submit(ActionBringup, head.fn("head", nil))
	head.waitEntered(t)

	kinds := []ActionKind{
		ActionRead, ActionEnable, ActionDisable, ActionBypass,
		ActionRead, ActionEnable, ActionDisable, ActionBypass,
		ActionRead, ActionTeardown,
	}
	var order []int
	var mu sync.Mutex
	replies := make([]<-chan ActionResult, len(kinds))
	for i, k := range kinds {
		replies[i] = q.Submit(k, func(ctx context.Context) (any, error) {
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			return i, nil
		})
	}

	head.open()
	_ = recvOrFail(t, headReply)
	for i, r := range replies {
		res := recvOrFail(t, r)
		if res.Err != nil {
			t.Fatalf("reply %d: unexpected err %v", i, res.Err)
		}
		if got, _ := res.Value.(int); got != i {
			t.Fatalf("reply %d: got value %v", i, res.Value)
		}
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprintf("%v", order) != fmt.Sprintf("%v", want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestActionQueue_CoalescingMatrix(t *testing.T) {
	// Mirrors the PRD coalescing table verbatim. Trailing reconciles
	// after teardown MUST execute — the rules store persists across
	// teardown, so a dropped post-teardown mutation would mean a user
	// who removed `evil.com` then shut the firewall down would find it
	// still allowed on next firewall up.
	cases := []struct {
		name    string
		submits []ActionKind
		want    []ActionKind
	}{
		{"single_reconcile", []ActionKind{ActionReconcile}, []ActionKind{ActionReconcile}},
		{
			"three_reconciles",
			[]ActionKind{ActionReconcile, ActionReconcile, ActionReconcile},
			[]ActionKind{ActionReconcile},
		},
		{
			"R_R_T",
			[]ActionKind{ActionReconcile, ActionReconcile, ActionTeardown},
			[]ActionKind{ActionReconcile, ActionTeardown},
		},
		{
			"R_R_R_T_R",
			[]ActionKind{ActionReconcile, ActionReconcile, ActionReconcile, ActionTeardown, ActionReconcile},
			[]ActionKind{ActionReconcile, ActionTeardown, ActionReconcile},
		},
		{"single_teardown", []ActionKind{ActionTeardown}, []ActionKind{ActionTeardown}},
		{
			"R_T_R",
			[]ActionKind{ActionReconcile, ActionTeardown, ActionReconcile},
			[]ActionKind{ActionReconcile, ActionTeardown, ActionReconcile},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := NewActionQueue(nil)
			defer func() { _ = q.Close() }()

			head := newGate()
			headReply := q.Submit(ActionBringup, head.fn(nil, nil))
			head.waitEntered(t)

			var mu sync.Mutex
			var order []ActionKind
			replies := make([]<-chan ActionResult, len(tc.submits))
			for i, k := range tc.submits {
				replies[i] = q.Submit(k, func(ctx context.Context) (any, error) {
					mu.Lock()
					order = append(order, k)
					mu.Unlock()
					return nil, nil
				})
			}

			head.open()
			_ = recvOrFail(t, headReply)
			for _, r := range replies {
				_ = recvOrFail(t, r)
			}

			mu.Lock()
			defer mu.Unlock()
			if fmt.Sprintf("%v", order) != fmt.Sprintf("%v", tc.want) {
				t.Fatalf("order = %v, want %v", order, tc.want)
			}
		})
	}
}

// TestActionQueue_CoalescedResultBroadcast pins two load-bearing
// invariants of the coalescing head-wins contract at once:
//
//  1. N coalesced Reconciles execute the HEAD closure exactly once —
//     peers do NOT run their own closure bodies (drop guarantee).
//  2. Every submitter receives the HEAD's ActionResult — the Value on
//     success, the Err on failure — not its own closure's return (fan-
//     out guarantee).
//
// Covers both success and failure paths in one table so a regression
// that (e.g.) leaks a peer's closure on the error branch surfaces here
// instead of living quietly behind a happy-path test.
func TestActionQueue_CoalescedResultBroadcast(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		boom error
	}{
		{"success_broadcasts_head_value", nil},
		{"failure_broadcasts_head_err", boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := NewActionQueue(nil)
			defer func() { _ = q.Close() }()

			// Hold the worker so all N Reconciles are queued before the
			// first one pops — the worker then coalesces them.
			head := newGate()
			headReply := q.Submit(ActionBringup, head.fn(nil, nil))
			head.waitEntered(t)

			const n = 5
			var runs int32
			replies := make([]<-chan ActionResult, n)
			for i := range n {
				replies[i] = q.Submit(ActionReconcile, func(ctx context.Context) (any, error) {
					atomic.AddInt32(&runs, 1)
					if tc.boom != nil {
						return nil, tc.boom
					}
					// Return i so the test can prove every submitter got
					// the HEAD's result, not its own closure's return.
					return i, nil
				})
			}

			head.open()
			_ = recvOrFail(t, headReply)
			for i, r := range replies {
				res := recvOrFail(t, r)
				if tc.boom != nil {
					if !errors.Is(res.Err, tc.boom) {
						t.Fatalf("reply %d: err = %v, want %v", i, res.Err, tc.boom)
					}
					if res.Value != nil {
						t.Fatalf("reply %d: value = %v, want nil on failure", i, res.Value)
					}
					continue
				}
				if res.Err != nil {
					t.Fatalf("reply %d: unexpected err %v", i, res.Err)
				}
				got, ok := res.Value.(int)
				if !ok {
					t.Fatalf("reply %d: value type = %T, want int", i, res.Value)
				}
				if got != 0 {
					t.Fatalf("reply %d: value = %d, want 0 (head's return, broadcast to all coalesced)", i, got)
				}
			}
			if got := atomic.LoadInt32(&runs); got != 1 {
				t.Fatalf("closure ran %d times, want 1 (N coalesced = 1 execution)", got)
			}
		})
	}
}

func TestActionQueue_PostCloseSubmitReturnsErrClosed(t *testing.T) {
	q := NewActionQueue(nil)
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Use a channel rather than t.Fatal inside the closure: t.Fatal in
	// a non-test goroutine calls runtime.Goexit on the worker, which
	// would strand q.done and hang every future test.
	ran := make(chan struct{}, 1)
	reply := q.Submit(ActionReconcile, func(ctx context.Context) (any, error) {
		ran <- struct{}{}
		return nil, nil
	})
	res := recvOrFail(t, reply)
	if !errors.Is(res.Err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", res.Err)
	}
	select {
	case <-ran:
		t.Fatal("closure ran after Close — submission should have been rejected pre-queue")
	default:
	}

	// Reply channel must be closed so ranging/second receives return
	// the zero value immediately rather than hanging.
	if _, ok := <-reply; ok {
		t.Fatal("reply channel should be closed after ErrClosed delivery")
	}
}

func TestActionQueue_SubmitNilClosureReturnsErrNilClosure(t *testing.T) {
	q := NewActionQueue(nil)
	defer func() { _ = q.Close() }()

	reply := q.Submit(ActionReconcile, nil)
	res := recvOrFail(t, reply)
	if !errors.Is(res.Err, ErrNilClosure) {
		t.Fatalf("err = %v, want ErrNilClosure", res.Err)
	}
}

func TestActionQueue_CloseWaitsForInFlight(t *testing.T) {
	q := NewActionQueue(nil)

	g := newGate()
	reply := q.Submit(ActionReconcile, g.fn("done", nil))
	g.waitEntered(t)

	closeReturned := make(chan struct{})
	go func() {
		_ = q.Close()
		close(closeReturned)
	}()

	// Close must not return while the closure is still running.
	select {
	case <-closeReturned:
		t.Fatal("Close returned before in-flight closure completed")
	case <-time.After(50 * time.Millisecond):
	}

	g.open()
	res := recvOrFail(t, reply)
	if res.Err != nil || res.Value != "done" {
		t.Fatalf("in-flight reply = %+v", res)
	}
	select {
	case <-closeReturned:
	case <-time.After(awaitTimeout):
		t.Fatal("Close did not return after closure completed")
	}
}

func TestActionQueue_CloseDrainsQueuedWork(t *testing.T) {
	q := NewActionQueue(nil)

	// Slow head holds the worker while we stack five more behind it.
	g := newGate()
	headReply := q.Submit(ActionBringup, g.fn("head", nil))
	g.waitEntered(t)

	const n = 5
	var ran int32
	replies := make([]<-chan ActionResult, n)
	for i := range n {
		replies[i] = q.Submit(ActionTeardown, func(ctx context.Context) (any, error) {
			atomic.AddInt32(&ran, 1)
			return i, nil
		})
	}

	closeReturned := make(chan struct{})
	go func() {
		_ = q.Close()
		close(closeReturned)
	}()

	// Close must not return while head is blocked — it has to drain
	// the five queued items too.
	select {
	case <-closeReturned:
		t.Fatal("Close returned before drain completed")
	case <-time.After(50 * time.Millisecond):
	}

	g.open()
	_ = recvOrFail(t, headReply)
	for i, r := range replies {
		res := recvOrFail(t, r)
		if errors.Is(res.Err, ErrClosed) {
			t.Fatalf("queued item %d received ErrClosed — Close dropped accepted work", i)
		}
		if res.Err != nil {
			t.Fatalf("queued item %d: err = %v", i, res.Err)
		}
		if got, _ := res.Value.(int); got != i {
			t.Fatalf("queued item %d: value = %v", i, res.Value)
		}
	}
	<-closeReturned
	if got := atomic.LoadInt32(&ran); got != n {
		t.Fatalf("ran %d closures, want %d", got, n)
	}
}

func TestActionQueue_CloseThenSubmitRace(t *testing.T) {
	q := NewActionQueue(nil)

	const submitters = 64
	var wg sync.WaitGroup
	results := make(chan ActionResult, submitters)

	for i := range submitters {
		wg.Go(func() {
			reply := q.Submit(ActionReconcile, func(ctx context.Context) (any, error) {
				return i, nil
			})
			results <- <-reply
		})
	}

	// Race Close against the submitters.
	go func() { _ = q.Close() }()

	wg.Wait()
	close(results)

	var ran, rejected int
	for res := range results {
		switch {
		case errors.Is(res.Err, ErrClosed):
			rejected++
		case res.Err == nil:
			// A ran submitter's closure returned `i, nil` — an int
			// boxed into any is non-nil even when i==0, but the
			// real invariant is "no error and the closure ran", so
			// just count it.
			ran++
		default:
			t.Fatalf("invalid result %+v — every submission must get ErrClosed or a real result", res)
		}
	}
	if ran+rejected != submitters {
		t.Fatalf("accounted %d of %d submissions", ran+rejected, submitters)
	}
}

func TestActionQueue_CloseCancelsInFlightContext(t *testing.T) {
	q := NewActionQueue(nil)

	started := make(chan struct{})
	observed := make(chan error, 1)
	reply := q.Submit(ActionReconcile, func(ctx context.Context) (any, error) {
		close(started)
		select {
		case <-ctx.Done():
			observed <- ctx.Err()
			return nil, ctx.Err()
		case <-time.After(awaitTimeout):
			observed <- errors.New("ctx never cancelled")
			return nil, errors.New("ctx never cancelled")
		}
	})

	<-started
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close must cancel the in-flight ctx promptly — a regression that
	// forgets to wire cancel into Close would still eventually pass
	// through awaitTimeout, so bound this assertion tightly so the
	// failure mode is clearly "cancel not wired" not "ctx never
	// cancelled."
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("closure saw err = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("closure did not observe ctx cancellation within 200ms of Close")
	}

	res := recvOrFail(t, reply)
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("reply err = %v, want context.Canceled", res.Err)
	}
}

func TestActionQueue_ClosurePanicDoesNotKillWorker(t *testing.T) {
	q := NewActionQueue(nil)
	defer func() { _ = q.Close() }()

	panicReply := q.Submit(ActionReconcile, func(ctx context.Context) (any, error) {
		panic("boom")
	})
	res := recvOrFail(t, panicReply)
	if !errors.Is(res.Err, ErrClosurePanic) {
		t.Fatalf("err = %v, want ErrClosurePanic", res.Err)
	}

	// Worker must still be alive and serving subsequent submissions —
	// a naive design that lets the panic kill the worker would hang
	// this second submission forever.
	nextReply := q.Submit(ActionRead, func(ctx context.Context) (any, error) {
		return "ok", nil
	})
	res = recvOrFail(t, nextReply)
	if res.Err != nil || res.Value != "ok" {
		t.Fatalf("post-panic submission result = %+v", res)
	}
}

// TestActionQueue_FailedClosureZeroesValue locks in the ActionResult
// contract: on failure Err is meaningful and Value is nil, regardless
// of whatever the closure happened to return alongside the error.
// Without enforcement in execute(), a closure returning (val, err)
// would leak `val` to every coalesced submitter.
func TestActionQueue_FailedClosureZeroesValue(t *testing.T) {
	q := NewActionQueue(nil)
	defer func() { _ = q.Close() }()

	boom := errors.New("closure failed")
	reply := q.Submit(ActionRead, func(ctx context.Context) (any, error) {
		// Deliberate: return both a non-nil Value and an error. The
		// queue must drop the Value before handing the result to the
		// submitter.
		return "leaked payload", boom
	})
	res := recvOrFail(t, reply)
	if !errors.Is(res.Err, boom) {
		t.Fatalf("err = %v, want %v", res.Err, boom)
	}
	if res.Value != nil {
		t.Fatalf("value = %v, want nil on failure (ActionResult contract)", res.Value)
	}
}
