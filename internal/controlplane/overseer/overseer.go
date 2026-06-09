package overseer

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// ErrClosed is returned by Snapshot after Close (or after the Start
// context cancelled). Publish under the same conditions returns false.
var ErrClosed = errors.New("overseer closed")

// ErrNotStarted is a defined sentinel for the not-yet-started state.
// No public function currently returns it: Publish, Subscribe, and
// Snapshot all return false/(zero,false) before Start. Retained for
// callers that want a typed sentinel to check against.
var ErrNotStarted = errors.New("overseer not started")

// Overseer is the typed event bus + in-memory worldview state
// maintained by the control plane. Construct with New, run the loop
// with Start, release resources with Close. Safe for concurrent
// Publish / Subscribe / Snapshot / Stats by any number of producers
// and consumers after Start returns.
//
// All mutable state (subscribers, State) is owned by a single goroutine
// — the run loop in run() — and accessed via channels. There is no
// per-field mutex: the loop is the serialization point. This mirrors
// the informer's writer-loop pattern but with simpler semantics
// (Publish is fire-and-forget; no synchronous result channel; no graph).
type Overseer struct {
	opts Options

	// publishCh carries events from any producer to the run loop. The
	// channel is NEVER closed — shutdown is signalled by stopCh, which
	// the loop watches. Closing a channel from the sender side races
	// with concurrent Publish callers (panic on send to closed).
	publishCh     chan Event
	subscribeCh   chan subscriptionReq
	unsubscribeCh chan unsubscribeReq
	snapshotCh    chan snapshotReq

	stopCh   chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	started atomic.Bool
	closed  atomic.Bool

	publishedTotal atomic.Uint64
	droppedTotal   atomic.Uint64
}

// New constructs an Overseer with opts. The run loop is not active
// until Start is called.
func New(opts Options) *Overseer {
	if opts.PublishBufferSize <= 0 {
		opts.PublishBufferSize = defaultPublishBuffer
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
	return &Overseer{
		opts:          opts,
		publishCh:     make(chan Event, opts.PublishBufferSize),
		subscribeCh:   make(chan subscriptionReq),
		unsubscribeCh: make(chan unsubscribeReq),
		snapshotCh:    make(chan snapshotReq),
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Start launches the run loop. Idempotent. Returns ErrClosed if called
// after Close. If ctx cancels, the bus transitions to the closed state
// identically to an explicit Close — Publish callers see false, in-
// flight subscribers see their channels closed.
func (o *Overseer) Start(ctx context.Context) error {
	if o.closed.Load() {
		return ErrClosed
	}
	if !o.started.CompareAndSwap(false, true) {
		return nil
	}
	go o.run(ctx)
	return nil
}

// Close stops accepting publishes, drains the queue, stops the run
// loop, and closes every subscriber channel. Idempotent. Blocks until
// the loop exits. Close on an unstarted Overseer flips the closed flag
// (so any held reference returns false on Publish) but doesn't drain a
// loop that never ran.
func (o *Overseer) Close() error {
	o.triggerShutdown()
	if o.started.Load() {
		<-o.done
	}
	o.opts.Logger.Info().Msg("overseer: closed")
	return nil
}

// Snapshot returns a deep copy of the current State. Returns
// (zero, false) if the bus is closed or ctx cancels before the request
// can be served.
func (o *Overseer) Snapshot(ctx context.Context) (State, bool) {
	if o.closed.Load() || !o.started.Load() {
		return State{}, false
	}
	resp := make(chan State, 1)
	select {
	case <-o.stopCh:
		return State{}, false
	case <-ctx.Done():
		return State{}, false
	case o.snapshotCh <- snapshotReq{resp: resp}:
	}
	select {
	case <-o.stopCh:
		return State{}, false
	case <-ctx.Done():
		return State{}, false
	case state := <-resp:
		return state, true
	}
}

// Stats returns counter snapshot. Cheap; safe under load.
func (o *Overseer) Stats() Stats {
	st := Stats{
		PublishedTotal: o.publishedTotal.Load(),
		DroppedTotal:   o.droppedTotal.Load(),
		QueueDepth:     len(o.publishCh),
		QueueCapacity:  cap(o.publishCh),
	}
	// Subscribers / known counts read via snapshot-style probe so the
	// run loop is the sole owner. Stats is called every 30s in the
	// CP heartbeat — the snapshot path is the canonical answer.
	if o.started.Load() && !o.closed.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		probe := make(chan statsProbeResp, 1)
		select {
		case o.subscribeCh <- subscriptionReq{statsProbe: probe}:
			select {
			case r := <-probe:
				st.Subscribers = r.subscribers
				st.ContainersKnown = r.containersKnown
				st.SessionsKnown = r.sessionsKnown
			case <-ctx.Done():
			}
		case <-ctx.Done():
		case <-o.stopCh:
		}
	}
	return st
}

// triggerShutdown flips closed=true and closes stopCh exactly once.
// Called by Close and by the run loop when its Start context cancels.
func (o *Overseer) triggerShutdown() {
	o.stopOnce.Do(func() {
		o.closed.Store(true)
		close(o.stopCh)
	})
}

// run is the single serialization point for state mutation, subscriber
// management, and event fan-out. Exits when stopCh closes or the Start
// ctx cancels. Drains the publishCh after shutdown so producers blocked
// in Publish observe the bus state immediately.
func (o *Overseer) run(ctx context.Context) {
	defer close(o.done)

	state := newState()
	subscribers := make(map[reflect.Type]map[uint64]*subscriber)
	var nextID uint64

	for {
		select {
		case <-o.stopCh:
			closeAllSubscribers(subscribers)
			return
		case <-ctx.Done():
			o.triggerShutdown()
			closeAllSubscribers(subscribers)
			return
		case req := <-o.subscribeCh:
			if req.statsProbe != nil {
				total := 0
				for _, group := range subscribers {
					total += len(group)
				}
				req.statsProbe <- statsProbeResp{
					subscribers:     total,
					containersKnown: len(state.Containers),
					sessionsKnown:   len(state.Agents),
				}
				continue
			}
			nextID++
			sub := &subscriber{
				id:        nextID,
				eventType: req.eventType,
				name:      req.name,
				filter:    req.filter,
				ch:        make(chan any, req.buffer),
			}
			if subscribers[req.eventType] == nil {
				subscribers[req.eventType] = make(map[uint64]*subscriber)
			}
			subscribers[req.eventType][sub.id] = sub
			req.resp <- subscriptionResp{id: sub.id, ch: sub.ch}
		case req := <-o.unsubscribeCh:
			group := subscribers[req.eventType]
			if group == nil {
				continue
			}
			sub, ok := group[req.id]
			if !ok {
				continue
			}
			delete(group, req.id)
			close(sub.ch)
			if len(group) == 0 {
				delete(subscribers, req.eventType)
			}
		case req := <-o.snapshotCh:
			req.resp <- state.clone()
		case ev := <-o.publishCh:
			o.applyAndDispatch(&state, subscribers, ev)
		}
	}
}

// applyAndDispatch runs one event through the state-applier hook (if
// the event implements applier) and then offers it to every subscriber
// registered against its concrete reflect.Type. Subscriber filters are
// evaluated here; a subscriber with a non-nil filter that returns false
// is skipped (no drop counted — the filter intentionally excluded the
// event, that's not back-pressure).
//
// Filter and ApplyTo are user-supplied closures that run on the bus
// loop goroutine. A panic in either would kill the bus and starve
// every subscriber. Each call site is guarded with a recover so a
// buggy event-handler is contained to that one event.
func (o *Overseer) applyAndDispatch(state *State, subscribers map[reflect.Type]map[uint64]*subscriber, ev Event) {
	o.publishedTotal.Add(1)
	o.safeApply(state, ev)
	o.safeHook(ev)
	group := subscribers[reflect.TypeOf(ev)]
	for _, sub := range group {
		if sub.filter != nil && !o.safeFilter(sub, ev) {
			continue
		}
		if !sub.offer(ev) {
			o.droppedTotal.Add(1)
			o.opts.Logger.Warn().
				Str("subscriber", sub.name).
				Str("event", ev.EventName()).
				Msg("overseer: subscriber dropped event (buffer full)")
		}
	}
}

// safeApply invokes the applier hook (if any) under a recover so a
// panicking ApplyTo cannot kill the bus loop.
func (o *Overseer) safeApply(state *State, ev Event) {
	a, ok := ev.(applier)
	if !ok {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			o.opts.Logger.Error().
				Interface("panic", r).
				Str("event", ev.EventName()).
				Msg("overseer: ApplyTo panicked; state may be inconsistent for this event")
		}
	}()
	a.ApplyTo(state)
	state.LastUpdatedAt = ev.OccurredAt()
}

// safeFilter invokes a subscriber's match predicate under a recover.
// Returns false on panic so the panicking subscriber simply doesn't
// receive the event; other subscribers continue to be served.
func (o *Overseer) safeFilter(sub *subscriber, ev Event) (matched bool) {
	defer func() {
		if r := recover(); r != nil {
			matched = false
			o.opts.Logger.Error().
				Interface("panic", r).
				Str("subscriber", sub.name).
				Str("event", ev.EventName()).
				Msg("overseer: subscriber filter panicked; skipping delivery")
		}
	}()
	return sub.filter(ev)
}

// safeHook invokes Options.PublishHook (if set) under a recover so a
// panicking hook is contained to the event that triggered it.
// Subsequent events are still processed.
func (o *Overseer) safeHook(ev Event) {
	if o.opts.PublishHook == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			o.opts.Logger.Error().
				Interface("panic", r).
				Str("event", ev.EventName()).
				Msg("overseer: PublishHook panicked; subsequent events unaffected")
		}
	}()
	o.opts.PublishHook(ev)
}

// NewLoggerHook returns a PublishHook that emits one structured Info
// line per event. The log message text IS the event name
// (e.g., "agent.session.connected") so log-stream views are
// scannable without expanding every entry — same identifying
// string is also kept on the `event` field for label-filter queries.
// occurred_at and the event's MarshalZerologObject payload land as
// structured fields so per-type identity (container_id, agent,
// project, address, registry outcomes, ...) is filterable and
// labelable.
func NewLoggerHook(log *logger.Logger) func(Event) {
	if log == nil {
		log = logger.Nop()
	}
	return func(ev Event) {
		log.Info().
			Str("event", ev.EventName()).
			Time("occurred_at", ev.OccurredAt()).
			EmbedObject(ev).
			Msg(ev.EventName())
	}
}

// closeAllSubscribers closes every typed channel and clears the map.
// Called once during shutdown.
func closeAllSubscribers(subscribers map[reflect.Type]map[uint64]*subscriber) {
	for t, group := range subscribers {
		for _, sub := range group {
			close(sub.ch)
		}
		delete(subscribers, t)
	}
}

type snapshotReq struct {
	resp chan State
}

type statsProbeResp struct {
	subscribers     int
	containersKnown int
	sessionsKnown   int
}
