package informer

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

//go:generate moq -rm -pkg mocks -out mocks/informer_mock.go . Interface

// ErrClosed is returned by write methods after Close has been called
// or after the Start context has cancelled. Both paths converge on the
// same shutdown state.
var ErrClosed = errors.New("informer closed")

// ErrNotStarted is returned by write methods submitted before Start.
// Without the guard, submit would enqueue into the queue with no
// consumer and waitOp would block until the caller's ctx cancels.
var ErrNotStarted = errors.New("informer not started")

// Interface is the full informer surface consumers depend on. A
// concrete Informer satisfies it; tests use the generated moq.
//
// The interface is consumer-shaped: it excludes Start/Close because
// those are owned by whichever component constructed the informer
// (typically cmd/clawker-cp). Consumers receive an Interface, not an
// *Informer, and therefore cannot accidentally shut it down.
type Interface interface {
	// Writes
	Upsert(ctx context.Context, u ResourceUpdate, t Transition) error
	Patch(ctx context.Context, key Key, fn func(*Resource), t Transition) error
	Remove(ctx context.Context, key Key, t Transition) error
	LinkRelation(ctx context.Context, rel Relation) error
	UnlinkRelation(ctx context.Context, from, to Key, kind string) error

	// Reads
	Get(key Key) (Resource, bool)
	List(f Filter) []Resource
	History(key Key) []Transition
	Neighbors(key Key, relKind string) []Resource
	Incoming(key Key, relKind string) []Resource

	// Subscriptions
	Subscribe(f Filter) (snapshot []Resource, ch <-chan Delta, cancel func())

	// Observability
	Stats() Stats
}

// Options configures an Informer. Zero values are valid.
type Options struct {
	// WriteQueueSize bounds the writer goroutine's input queue.
	// Defaults to 1024. Feeders block when the queue is full —
	// backpressure flows up to the source (Moby's events channel,
	// agent RPC handler, etc.), which is the correct behaviour.
	WriteQueueSize int
	// SubscriberBuffer bounds each subscriber's delta channel.
	// Defaults to 128. Full buffer → drop-oldest, increment
	// DeltasDroppedTotal. Informer never blocks on slow subscribers.
	SubscriberBuffer int
	// Logger receives audit events (every write, every subscriber
	// drop, every close). Nil defaults to logger.Nop().
	Logger *logger.Logger
	// Now is an injectable clock for deterministic tests. Defaults
	// to time.Now.
	Now func() time.Time
}

const (
	defaultWriteQueueSize   = 1024
	defaultSubscriberBuffer = 128
)

// Informer is a push-fed, in-process realm model. Construct with New,
// start the writer goroutine with Start, release resources with Close.
// Safe for concurrent use by any number of feeders and consumers after
// Start returns.
type Informer struct {
	opts Options

	mu    sync.RWMutex // guards store mutation and subscriberSet membership; see apply() for close/offer ordering
	store *store
	subs  subscriberSet

	// queue is the writer input. It is NEVER closed — shutdown is
	// signalled by closing stopCh. Closing queue from the sender side
	// would race with concurrent submit() callers (send on closed
	// channel panic).
	queue chan op
	// stopCh is closed exactly once to signal shutdown to both the
	// writer loop and any submit() in flight. Guarded by stopOnce.
	stopCh   chan struct{}
	stopOnce sync.Once
	// done is closed by the writer goroutine when it has fully exited
	// (including the post-stop drain). Close() blocks on it.
	done chan struct{}

	started atomic.Bool
	closed  atomic.Bool

	// counters
	writesTotal   atomic.Uint64
	deltasEmitted atomic.Uint64
	deltasDropped atomic.Uint64
}

// Compile-time check that *Informer satisfies the consumer Interface.
var _ Interface = (*Informer)(nil)

// New constructs an Informer with opts. The writer goroutine is not
// running until Start is called.
func New(opts Options) *Informer {
	if opts.WriteQueueSize <= 0 {
		opts.WriteQueueSize = defaultWriteQueueSize
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = defaultSubscriberBuffer
	}
	if opts.Logger == nil {
		opts.Logger = logger.Nop()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Informer{
		opts:   opts,
		store:  newStore(),
		subs:   newSubscriberSet(),
		queue:  make(chan op, opts.WriteQueueSize),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start launches the writer goroutine. Safe to call once. Returns an
// error if called after Close. Subsequent Start calls are no-ops.
//
// If ctx cancels, the informer transitions to the closed state
// identically to an explicit Close call: subsequent writes see
// ErrClosed; in-flight writes either drain (if already enqueued) or
// return ErrClosed.
func (i *Informer) Start(ctx context.Context) error {
	if i.closed.Load() {
		return ErrClosed
	}
	if !i.started.CompareAndSwap(false, true) {
		return nil
	}
	go i.writerLoop(ctx)
	return nil
}

// Close stops accepting writes, drains any ops already enqueued,
// stops the writer goroutine, and closes every subscriber channel.
// Safe to call multiple times; idempotent. Close blocks until the
// writer goroutine exits. Close on an unstarted informer is a no-op
// on the writer side (there is no goroutine to drain) but still
// closes subscribers and flips the closed flag so any held
// Interface reference returns ErrClosed on subsequent writes.
func (i *Informer) Close() error {
	i.triggerShutdown()
	if i.started.Load() {
		<-i.done
	}
	i.mu.Lock()
	i.subs.closeAll()
	i.mu.Unlock()
	i.opts.Logger.Info().Msg("informer: closed")
	return nil
}

// triggerShutdown flips closed=true and closes stopCh exactly once.
// Called by Close and by the writer loop when its Start context
// cancels — both paths converge on the same teardown state so
// feeders never deadlock waiting for a writer that has silently gone
// away (see "writerLoop ctx-cancel leak" history).
func (i *Informer) triggerShutdown() {
	i.stopOnce.Do(func() {
		i.closed.Store(true)
		close(i.stopCh)
	})
}

// writerLoop is the single serialization point for mutations. It
// reads ops from the queue, commits them under the store write lock,
// and fans out deltas to subscribers. Exits when stopCh closes (via
// Close or ctx cancellation), after draining any already-enqueued ops
// so feeders blocked on waitOp see their write committed.
func (i *Informer) writerLoop(ctx context.Context) {
	defer close(i.done)
	for {
		select {
		case <-i.stopCh:
			i.drainAfterStop()
			return
		case <-ctx.Done():
			// Start context cancelled. Flip to shutdown state so any
			// concurrent submit() observes ErrClosed via stopCh rather
			// than blocking on a writer that is about to exit.
			i.triggerShutdown()
			i.drainAfterStop()
			return
		case op := <-i.queue:
			i.apply(op)
		}
	}
}

// drainAfterStop applies every op already in the queue after shutdown
// was signalled. Submitters in flight either won the race against
// stopCh in submit() (ops are here, apply them so waitOp unblocks) or
// lost and returned ErrClosed (nothing to drain for them). Either way,
// no op leaks a forever-blocked waitOp. Non-blocking: we never wait on
// the queue — stopCh means no new sends can succeed.
func (i *Informer) drainAfterStop() {
	for {
		select {
		case op := <-i.queue:
			i.apply(op)
		default:
			return
		}
	}
}

// apply commits one op and fans out any resulting delta. Fan-out
// happens under the write lock so cancel()/closeAll, which take the
// same lock, serialize strictly after any in-flight offer(). offer is
// non-blocking (drop-oldest), so the lock is held only briefly per
// delta. Without this, a cancel could close a subscriber's channel
// between offer's atomic-flag check and its send → panic.
//
// The op result is signalled outside the lock on a cap-1 buffered
// channel so a caller blocked on waitOp does not hold the writer. The
// buffered-by-1 invariant is load-bearing: a future change to an
// unbuffered result would deadlock every waiter.
func (i *Informer) apply(o op) {
	now := i.opts.Now()

	i.mu.Lock()
	delta, emitted := o.fn(i.store, now)
	i.writesTotal.Add(1)
	if emitted {
		i.deltasEmitted.Add(1)
		for _, s := range i.subs.byID {
			if ok := s.offer(delta); !ok {
				i.deltasDropped.Add(1)
				i.opts.Logger.Warn().
					Str("subscriber", s.name).
					Str("delta_kind", delta.Kind.String()).
					Int("filter_kinds", len(s.filter.Kinds)).
					Int("filter_labels", len(s.filter.Labels.Equals)+len(s.filter.Labels.NotEquals)+
						len(s.filter.Labels.Exists)+len(s.filter.Labels.NotExists)).
					Int("filter_attrs", len(s.filter.AttrsMatch)).
					Msg("informer: subscriber dropped delta (buffer full)")
			}
		}
	}
	i.mu.Unlock()

	if emitted {
		i.logPublished(delta)
	}

	if o.result != nil {
		o.result <- opResult{delta: delta, emitted: emitted}
		close(o.result)
	}
}

// logPublished emits one structured info-level log line per committed
// Delta. The body is the Delta's canonical JSON form (see
// Delta.MarshalJSON in types.go) — no projection or transformation
// in this function. Centralised here so every feeder gets the
// publish trail without per-feeder duplication; a missing publish
// log under a docker event receive line means the dispatch path
// filtered the event before it reached an informer write.
func (i *Informer) logPublished(d Delta) {
	body, _ := json.Marshal(d)
	i.opts.Logger.Info().
		RawJSON("delta", body).
		Msgf("informer published: %s", body)
}

// op is one queued mutation. fn runs under the write lock and returns
// the delta plus whether it should be fanned out. result receives the
// outcome (may be nil for fire-and-forget writes).
type op struct {
	fn     func(s *store, now time.Time) (Delta, bool)
	result chan opResult
}

type opResult struct {
	delta   Delta
	emitted bool
}

// submit enqueues o. Returns ErrNotStarted if Start has not been
// called (submitting before Start would block forever waiting on
// waitOp — no writer is draining). Returns ErrClosed if the informer
// has shut down, or ctx.Err() if the caller's context cancels before
// the op enqueues. Does not wait for the op to execute — call waitOp
// on the returned result channel for that.
func (i *Informer) submit(ctx context.Context, o op) error {
	if !i.started.Load() {
		return ErrNotStarted
	}
	// Fast-path closed check. The authoritative shutdown signal is
	// stopCh in the select below — a concurrent Close() may flip
	// closed=true after this load but before the select, and stopCh
	// will carry us out of the send branch safely.
	if i.closed.Load() {
		return ErrClosed
	}
	select {
	case i.queue <- o:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-i.stopCh:
		// Informer shut down while we were racing the queue send.
		// Never closed the queue — so no send-on-closed panic is
		// possible even if stopCh and the send fire simultaneously.
		return ErrClosed
	}
}

// waitOp blocks for the op result. Returns an error if ctx cancels.
// The op may have partially executed at that point — ctx is advisory
// for the caller, not for the writer goroutine.
func waitOp(ctx context.Context, result <-chan opResult) (opResult, error) {
	select {
	case r, ok := <-result:
		if !ok {
			return opResult{}, nil
		}
		return r, nil
	case <-ctx.Done():
		return opResult{}, ctx.Err()
	}
}
