package firewall

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/schmitthub/clawker/internal/logger"
)

// Sentinels returned on an ActionResult when the queue itself rejects or
// cannot complete a submission. Closure-level failures use sentinels
// defined alongside the closure.
var (
	ErrClosed       = errors.New("action queue closed")
	ErrNilClosure   = errors.New("action queue: nil closure")
	ErrClosurePanic = errors.New("action closure panicked")
)

// ActionKind classifies a queued action. Its Coalesces method decides
// whether consecutive same-kind items collapse into a single execution.
type ActionKind int

const (
	ActionUnknown ActionKind = iota
	ActionBringup
	ActionTeardown
	ActionReconcile
	ActionRead
	ActionEnable
	ActionDisable
	ActionBypass
)

func (k ActionKind) String() string {
	switch k {
	case ActionBringup:
		return "bringup"
	case ActionTeardown:
		return "teardown"
	case ActionReconcile:
		return "reconcile"
	case ActionRead:
		return "read"
	case ActionEnable:
		return "enable"
	case ActionDisable:
		return "disable"
	case ActionBypass:
		return "bypass"
	default:
		return "unknown"
	}
}

// Coalesces reports whether consecutive submissions of this kind collapse
// into a single execution. Only ActionReconcile coalesces: every RPC
// mapped to it regenerates stack state from the current rules store, so
// drained submitters observe identical output. Per-container kinds
// (Enable, Disable, Bypass) carry distinct arguments per call and must
// run individually; Bringup, Teardown, and Read likewise execute one-by-one.
func (k ActionKind) Coalesces() bool {
	return k == ActionReconcile
}

// ActionResult is produced by a queued closure and delivered to every
// submitter whose action contributed to the execution. Exactly one of
// Value or Err is meaningful: on clean success Value carries the closure
// Result and Err is nil; on failure Err carries one or more wrapped
// sentinels and Value is nil.
type ActionResult struct {
	Value any
	Err   error
}

// ActionFunc is the shape of every queued closure. It receives the
// queue's long-lived context — cancelled by Close — and returns a Result
// value on success or a wrapped sentinel error on failure. Closures that
// run for more than a few milliseconds MUST honor ctx.Done() so Close can
// drain in bounded time.
type ActionFunc func(ctx context.Context) (any, error)

type actionItem struct {
	kind  ActionKind
	fn    ActionFunc
	reply chan ActionResult
}

// ActionQueue serializes firewall actions behind a single worker
// goroutine. Items execute in strict FIFO order across kinds; consecutive
// items whose kind returns true from Coalesces collapse into one
// execution and every drained submitter receives the same result. Submit
// is close-safe: submissions accepted before Close returned run to
// completion, submissions made after Close returned receive ErrClosed.
type ActionQueue struct {
	log    *logger.Logger
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	cond   *sync.Cond
	buffer []actionItem
	closed bool
}

// NewActionQueue constructs an ActionQueue and starts its worker
// goroutine. A nil logger is treated as logger.Nop().
func NewActionQueue(log *logger.Logger) *ActionQueue {
	if log == nil {
		log = logger.Nop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &ActionQueue{
		log:    log,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	q.cond = sync.NewCond(&q.mu)
	go q.run()
	return q
}

// Submit enqueues fn for execution under kind and returns a channel that
// will receive exactly one ActionResult. Submissions made after Close
// returned receive a pre-closed channel carrying ErrClosed; a nil fn
// likewise receives ErrNilClosure. Submit never panics and never waits
// on the worker to drain (the reply channel is buffered).
func (q *ActionQueue) Submit(kind ActionKind, fn ActionFunc) <-chan ActionResult {
	reply := make(chan ActionResult, 1)
	if fn == nil {
		reply <- ActionResult{Err: ErrNilClosure}
		close(reply)
		return reply
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		reply <- ActionResult{Err: ErrClosed}
		close(reply)
		return reply
	}
	q.buffer = append(q.buffer, actionItem{kind: kind, fn: fn, reply: reply})
	q.cond.Signal()
	q.mu.Unlock()
	return reply
}

// Close stops accepting new submissions, waits for every submission
// accepted before Close returned to run to completion, and cancels the
// worker context so in-flight and drained closures can observe shutdown.
// Drained closures execute with an already-cancelled context; ctx-aware
// closures that short-circuit on cancellation will deliver ctx.Err() to
// their submitters. Idempotent. Always returns nil — the error return
// matches io.Closer so callers can treat the queue as a Closer.
func (q *ActionQueue) Close() error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		<-q.done
		return nil
	}
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
	q.cancel()
	<-q.done
	return nil
}

// run is the worker loop: pop one item, coalesce consecutive peers of
// the same coalescing kind, execute the head closure once, and fan the
// result out to every coalesced submitter.
func (q *ActionQueue) run() {
	defer close(q.done)
	for {
		head, coalesced, ok := q.next()
		if !ok {
			return
		}
		result := q.execute(head)
		for _, reply := range coalesced {
			reply <- result
			close(reply)
		}
	}
}

// next blocks until an item is available or the queue is closed and
// drained. It pops the head and any consecutive peers whose kind
// coalesces. ok=false signals worker exit.
func (q *ActionQueue) next() (actionItem, []chan ActionResult, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buffer) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.buffer) == 0 {
		return actionItem{}, nil, false
	}
	head := q.popHead()
	replies := []chan ActionResult{head.reply}
	if head.kind.Coalesces() {
		for len(q.buffer) > 0 && q.buffer[0].kind == head.kind {
			peer := q.popHead()
			replies = append(replies, peer.reply)
		}
	}
	return head, replies, true
}

// popHead removes the front item and zeroes the vacated slot so the
// buffer's underlying array does not retain references to processed
// closures for the remaining lifetime of the queue.
func (q *ActionQueue) popHead() actionItem {
	head := q.buffer[0]
	q.buffer[0] = actionItem{}
	q.buffer = q.buffer[1:]
	return head
}

// execute runs the closure with the queue context, recovering from any
// panic so a single misbehaving closure cannot kill the worker — which
// would strand every queued item and every coalesced peer's reply
// channel. The recovered panic becomes an ActionResult wrapping
// ErrClosurePanic that is fanned out to every submitter.
func (q *ActionQueue) execute(item actionItem) (res ActionResult) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			q.log.Error().
				Str("kind", item.kind.String()).
				Interface("panic", r).
				Bytes("stack", stack).
				Msg("action queue closure panicked")
			res = ActionResult{Err: fmt.Errorf("%w: %v", ErrClosurePanic, r)}
		}
	}()
	val, err := item.fn(q.ctx)
	return ActionResult{Value: val, Err: err}
}
