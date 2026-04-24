package informer

import (
	"fmt"
	"slices"
	"sync/atomic"
)

// subscriber holds one consumer's delta channel + filter.
// The filter is the informer's private deep copy of the caller's
// Filter — mutating the caller's original maps after Subscribe does
// not affect delta routing. See apply() for the close/offer ordering
// invariant (fan-out holds i.mu so cancel can't close mid-send).
type subscriber struct {
	name   string
	filter Filter
	ch     chan Delta
	closed atomic.Bool
}

// offer attempts a non-blocking send. If the channel is full, the
// oldest delta is dropped and the new one takes its place —
// "drop-oldest". Returns false on drop, true on clean send.
func (s *subscriber) offer(d Delta) bool {
	if s.closed.Load() {
		return true // subscriber already gone; not a drop
	}
	if !s.filter.matches(d.After) && !s.filter.matches(d.Before) && !subscriberFilterAllowsRelation(s.filter, d) {
		// Filter excludes both sides; relation-kind deltas pass only
		// when the filter is empty (no way to match relation shape
		// against a resource filter). v1 keeps this simple.
		return true
	}
	select {
	case s.ch <- d:
		return true
	default:
		// drop-oldest: pop one, push new.
		select {
		case <-s.ch:
		default:
		}
		select {
		case s.ch <- d:
		default:
		}
		return false
	}
}

// subscriberFilterAllowsRelation returns true only when the filter is
// the zero value — a relation delta carries no resource payload, so a
// non-empty resource filter cannot match it.
func subscriberFilterAllowsRelation(f Filter, d Delta) bool {
	if d.Kind != DeltaRelationAdded && d.Kind != DeltaRelationRemoved {
		return false
	}
	return len(f.Kinds) == 0 && len(f.Lifecycles) == 0 &&
		len(f.AttrsMatch) == 0 &&
		len(f.Labels.Equals) == 0 && len(f.Labels.NotEquals) == 0 &&
		len(f.Labels.Exists) == 0 && len(f.Labels.NotExists) == 0
}

func (s *subscriber) close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.ch)
	}
}

// subscriberSet holds active subscribers. Guarded externally by
// Informer.mu.
type subscriberSet struct {
	byID map[uint64]*subscriber
	next uint64
}

func newSubscriberSet() subscriberSet {
	return subscriberSet{byID: make(map[uint64]*subscriber)}
}

func (ss *subscriberSet) add(s *subscriber) uint64 {
	ss.next++
	id := ss.next
	ss.byID[id] = s
	return id
}

func (ss *subscriberSet) remove(id uint64) *subscriber {
	s := ss.byID[id]
	delete(ss.byID, id)
	return s
}

func (ss *subscriberSet) len() int { return len(ss.byID) }

func (ss *subscriberSet) closeAll() {
	for _, s := range ss.byID {
		s.close()
	}
	ss.byID = make(map[uint64]*subscriber)
}

// Subscribe registers a new consumer. It returns (1) a snapshot of
// the current resource set that matches f, (2) a channel that emits
// every subsequent delta matching f, and (3) a cancel function to
// remove the subscription.
//
// Snapshot + channel are produced atomically: a delta that races the
// Subscribe call is either reflected in the snapshot or the channel,
// never both and never neither.
//
// The filter is copied — the caller may mutate their local Filter
// after Subscribe without affecting delivery.
//
// The channel is closed on cancel() or on Close. Consumers must drain
// or accept drop-oldest semantics.
//
// The subscriber is named "sub-N" for drop-attribution in logs.
// Consumers that want a human-readable identity on drop warnings use
// SubscribeNamed.
func (i *Informer) Subscribe(f Filter) ([]Resource, <-chan Delta, func()) {
	return i.SubscribeNamed("", f)
}

// SubscribeNamed is Subscribe with an explicit consumer identity. The
// name appears on every "subscriber dropped delta" log line so
// operators can attribute buffer pressure to a specific feeder or
// consumer instead of an opaque "sub-42". An empty name falls back to
// "sub-N" where N is the monotonic subscriber ID.
func (i *Informer) SubscribeNamed(name string, f Filter) ([]Resource, <-chan Delta, func()) {
	s := &subscriber{
		filter: copyFilter(f),
		ch:     make(chan Delta, i.opts.SubscriberBuffer),
	}

	i.mu.Lock()
	snapshot := listLocked(i.store, f)
	id := i.subs.add(s)
	if name != "" {
		s.name = name
	} else {
		s.name = fmt.Sprintf("sub-%d", id)
	}
	i.mu.Unlock()

	cancel := func() {
		i.mu.Lock()
		if removed := i.subs.remove(id); removed != nil {
			removed.close()
		}
		i.mu.Unlock()
	}
	return snapshot, s.ch, cancel
}

// copyFilter returns a deep copy of f. Guards against a caller that
// mutates their filter maps after Subscribe — without the copy, a
// concurrent write to f.Labels.Equals would race with matches().
func copyFilter(f Filter) Filter {
	out := Filter{
		Kinds:      slices.Clone(f.Kinds),
		Lifecycles: slices.Clone(f.Lifecycles),
		Labels: LabelSelector{
			Equals:    copyStringMap(f.Labels.Equals),
			NotEquals: copyStringMap(f.Labels.NotEquals),
			Exists:    slices.Clone(f.Labels.Exists),
			NotExists: slices.Clone(f.Labels.NotExists),
		},
		AttrsMatch: copyStringMap(f.AttrsMatch),
	}
	return out
}
