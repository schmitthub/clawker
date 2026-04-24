package informer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

//go:generate moq -rm -pkg mocks -out mocks/informer_mock.go . Interface

// ErrClosed is returned by write methods after Close has been called.
var ErrClosed = errors.New("informer closed")

// Interface is the full informer surface consumers depend on. A
// concrete Informer satisfies it; tests use the generated moq.
//
// The interface is consumer-shaped: it excludes Start/Close because
// those are owned by whichever component constructed the informer
// (typically cmd/clawker-cp). Consumers receive an Interface, not an
// *Informer, and therefore cannot accidentally shut it down.
type Interface interface {
	// Writes
	Upsert(ctx context.Context, r Resource, t Transition) error
	Patch(ctx context.Context, key Key, fn func(*Resource), t Transition) error
	Remove(ctx context.Context, key Key, t Transition) error
	LinkRelation(ctx context.Context, rel Relation, t Transition) error
	UnlinkRelation(ctx context.Context, from, to Key, kind string, t Transition) error

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

	mu    sync.RWMutex // guards store, subs
	store *store
	subs  subscriberSet

	queue    chan op
	queueCap int

	started atomic.Bool
	closed  atomic.Bool
	done    chan struct{}

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
		opts:     opts,
		store:    newStore(),
		subs:     newSubscriberSet(),
		queue:    make(chan op, opts.WriteQueueSize),
		queueCap: opts.WriteQueueSize,
		done:     make(chan struct{}),
	}
}

// Start launches the writer goroutine. Safe to call once. Returns an
// error if called after Close. Subsequent Start calls are no-ops.
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

// Close drains pending writes, stops the writer goroutine, and closes
// every subscriber channel. Safe to call multiple times; idempotent.
// Close blocks until the writer goroutine exits.
func (i *Informer) Close() error {
	if !i.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(i.queue)
	<-i.done
	i.mu.Lock()
	i.subs.closeAll()
	i.mu.Unlock()
	i.opts.Logger.Info().Msg("informer: closed")
	return nil
}

// writerLoop is the single serialization point for mutations. It
// reads ops from the queue, commits them under the store write lock,
// and fans out deltas to subscribers.
func (i *Informer) writerLoop(ctx context.Context) {
	defer close(i.done)
	for {
		select {
		case <-ctx.Done():
			// Caller's context cancelled. Drain remaining queued ops so
			// in-flight feeder writes complete, then exit. We do not
			// close the queue here — that's Close's job.
			i.drainAndExit()
			return
		case op, ok := <-i.queue:
			if !ok {
				return
			}
			i.apply(op)
		}
	}
}

func (i *Informer) drainAndExit() {
	for {
		select {
		case op, ok := <-i.queue:
			if !ok {
				return
			}
			i.apply(op)
		default:
			return
		}
	}
}

// apply commits one op and fans out any resulting delta. The result
// channel on the op, if non-nil, is signalled with the outcome.
func (i *Informer) apply(o op) {
	now := i.opts.Now()

	i.mu.Lock()
	delta, emitted := o.fn(i.store, now)
	// Snapshot subscriber list under the write lock so fan-out sees a
	// coherent set of subs for this op. Copy is cheap (pointer slice).
	subs := i.subs.snapshot()
	i.mu.Unlock()

	i.writesTotal.Add(1)

	if emitted {
		i.deltasEmitted.Add(1)
		for _, s := range subs {
			if ok := s.offer(delta); !ok {
				i.deltasDropped.Add(1)
				i.opts.Logger.Warn().
					Str("subscriber", s.name).
					Str("delta_kind", delta.Kind.String()).
					Msg("informer: subscriber dropped delta (buffer full)")
			}
		}
	}

	if o.result != nil {
		o.result <- opResult{delta: delta, emitted: emitted}
		close(o.result)
	}
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

// submit enqueues o. Returns ErrClosed if the informer is shut down,
// or ctx.Err() if the caller's context cancels before the op enqueues.
// Does not wait for the op to execute — call waitOp on the returned
// result channel for that.
func (i *Informer) submit(ctx context.Context, o op) error {
	if i.closed.Load() {
		return ErrClosed
	}
	select {
	case i.queue <- o:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
