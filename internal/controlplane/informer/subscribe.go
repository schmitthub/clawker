package informer

import (
	"fmt"
	"sync/atomic"
)

// subscriber holds one consumer's delta channel + filter.
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

func (ss *subscriberSet) snapshot() []*subscriber {
	out := make([]*subscriber, 0, len(ss.byID))
	for _, s := range ss.byID {
		out = append(out, s)
	}
	return out
}

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
// The channel is closed on cancel() or on Close. Consumers must drain
// or accept drop-oldest semantics.
func (i *Informer) Subscribe(f Filter) ([]Resource, <-chan Delta, func()) {
	s := &subscriber{
		filter: f,
		ch:     make(chan Delta, i.opts.SubscriberBuffer),
	}

	i.mu.Lock()
	snapshot := listLocked(i.store, f)
	id := i.subs.add(s)
	s.name = fmt.Sprintf("sub-%d", id)
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
