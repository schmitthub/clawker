package overseer

import (
	"fmt"
	"reflect"
)

// Subscription is the consumer-side handle to a typed event channel.
// Range over C to receive events; call Unsubscribe to remove the
// subscription (closes C). Closing C also happens on Overseer.Close.
type Subscription[T Event] struct {
	C           <-chan T
	unsubscribe func()
}

// Unsubscribe removes the subscription and closes C. Idempotent.
func (s Subscription[T]) Unsubscribe() {
	if s.unsubscribe != nil {
		s.unsubscribe()
	}
}

// Publish enqueues ev on the bus. Returns true if the event was
// queued, false if the bus is closed or its publish buffer is full
// (back-pressure: producers can react to a full bus by dropping or
// blocking themselves; the bus itself is non-blocking from the
// producer's perspective).
//
// Publish is type-safe at the call site (compile-time T) and
// dispatches via reflect.TypeOf inside the bus.
func Publish[T Event](o *Overseer, ev T) bool {
	if o.closed.Load() {
		return false
	}
	if !o.started.Load() {
		// Without a running loop nothing drains; queueing would be a
		// silent black hole. Return false so the producer can react.
		return false
	}
	select {
	case <-o.stopCh:
		return false
	case o.publishCh <- ev:
		return true
	default:
		// Buffer full. Account as a drop and let the producer decide
		// what to do with the false return.
		o.droppedTotal.Add(1)
		o.opts.Logger.Warn().
			Str("event", ev.EventName()).
			Int("queue_capacity", cap(o.publishCh)).
			Msg("overseer: publish dropped (queue full)")
		return false
	}
}

// Subscribe registers a consumer for events of type T. Returns the
// subscription with a typed receive-only channel of buffer size given
// in opts.SubscriberBuffer. The bool result is false if the bus is
// closed.
//
// Buffer overflow on a single subscriber drops events and increments
// DroppedTotal — the bus never blocks on a slow consumer.
func Subscribe[T Event](o *Overseer, name string) (Subscription[T], bool) {
	return SubscribeFiltered[T](o, name, nil)
}

// SubscribeFiltered registers a consumer for events of type T whose
// match predicate returns true. A nil match accepts every event of
// type T (equivalent to Subscribe).
//
// The predicate runs inside the bus loop, so it must be cheap and
// must not call back into the bus (would deadlock).
func SubscribeFiltered[T Event](o *Overseer, name string, match func(T) bool) (Subscription[T], bool) {
	if o.closed.Load() || !o.started.Load() {
		return Subscription[T]{}, false
	}

	var zero T
	eventType := reflect.TypeOf(zero)
	if eventType == nil {
		// T is an interface, not a concrete type. The bus keys
		// subscribers by concrete reflect.Type — interface T would
		// never match because dispatched events have concrete types.
		// Surface the misuse loudly.
		panic("overseer: Subscribe[T] requires a concrete event type, not an interface")
	}

	var rawFilter func(any) bool
	if match != nil {
		rawFilter = func(v any) bool {
			ev, ok := v.(T)
			return ok && match(ev)
		}
	}

	resp := make(chan subscriptionResp, 1)
	req := subscriptionReq{
		eventType: eventType,
		name:      defaultSubscriberName(name, eventType),
		filter:    rawFilter,
		buffer:    o.opts.SubscriberBuffer,
		resp:      resp,
	}

	select {
	case <-o.stopCh:
		return Subscription[T]{}, false
	case o.subscribeCh <- req:
	}

	registration := <-resp
	out := make(chan T, o.opts.SubscriberBuffer)

	go func() {
		// Convert untyped events to typed T. Goroutine exits when the
		// underlying ch closes (Unsubscribe or bus shutdown).
		defer close(out)
		for v := range registration.ch {
			ev, ok := v.(T)
			if !ok {
				// Should never happen: bus keys by reflect.TypeOf, so
				// a value on the typed subscriber's channel is always
				// a T. Defensive — drop silently to keep the loop
				// alive.
				continue
			}
			out <- ev
		}
	}()

	unsub := func() {
		select {
		case <-o.stopCh:
			return
		case o.unsubscribeCh <- unsubscribeReq{eventType: eventType, id: registration.id}:
		}
	}

	return Subscription[T]{C: out, unsubscribe: unsub}, true
}

// subscriber is the internal record for one registered consumer.
// Lives inside the bus's run loop; never escapes.
type subscriber struct {
	id        uint64
	eventType reflect.Type
	name      string
	filter    func(any) bool
	ch        chan any
}

// offer attempts a non-blocking send. On full buffer, drops the
// oldest event and pushes the new one (drop-oldest). Returns false on drop.
func (s *subscriber) offer(ev Event) bool {
	select {
	case s.ch <- ev:
		return true
	default:
		// Pop one, push new.
		select {
		case <-s.ch:
		default:
		}
		select {
		case s.ch <- ev:
		default:
		}
		return false
	}
}

// subscriptionReq carries one subscribe request from a Subscribe
// caller into the run loop. The statsProbe field overloads the same
// channel for cheap "how many subscribers" queries — Stats() needs
// the loop's view without taking an extra channel.
type subscriptionReq struct {
	eventType  reflect.Type
	name       string
	filter     func(any) bool
	buffer     int
	resp       chan subscriptionResp
	statsProbe chan statsProbeResp
}

type subscriptionResp struct {
	id uint64
	ch chan any
}

type unsubscribeReq struct {
	eventType reflect.Type
	id        uint64
}

func defaultSubscriberName(name string, t reflect.Type) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("sub-%s", t.String())
}
