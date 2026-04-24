package informer

// Get returns a deep copy of the resource under key and whether it
// exists. Returned value may be retained and mutated by the caller.
func (i *Informer) Get(key Key) (Resource, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	r := i.store.get(key)
	if r == nil {
		return Resource{}, false
	}
	return *copyResource(r), true
}

// List returns every resource matching f, deep-copied. Order is
// unspecified.
func (i *Informer) List(f Filter) []Resource {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return listLocked(i.store, f)
}

// listLocked is the shared implementation of List used by Subscribe
// to produce its initial snapshot under the write lock.
func listLocked(s *store, f Filter) []Resource {
	// Short-circuit when Kinds narrows the iteration surface.
	var out []Resource
	if len(f.Kinds) > 0 {
		for _, k := range f.Kinds {
			bucket := s.byKind[k]
			for _, r := range bucket {
				if f.matches(r) {
					out = append(out, *copyResource(r))
				}
			}
		}
		return out
	}
	for _, r := range s.resources {
		if f.matches(r) {
			out = append(out, *copyResource(r))
		}
	}
	return out
}

// History returns a copy of the transition history for the resource
// under key. Returns nil if the key is unknown.
func (i *Informer) History(key Key) []Transition {
	i.mu.RLock()
	defer i.mu.RUnlock()
	r := i.store.get(key)
	if r == nil || len(r.History) == 0 {
		return nil
	}
	out := make([]Transition, len(r.History))
	for idx, t := range r.History {
		t.Attrs = copyStringMap(t.Attrs)
		out[idx] = t
	}
	return out
}

// Neighbors lists outbound edges of key. An empty relKind returns
// edges of every kind.
func (i *Informer) Neighbors(key Key, relKind string) []Resource {
	i.mu.RLock()
	defer i.mu.RUnlock()
	n := i.store.neighbors(key, relKind)
	out := make([]Resource, 0, len(n))
	for _, r := range n {
		out = append(out, *r)
	}
	return out
}

// Incoming lists inbound edges of key.
func (i *Informer) Incoming(key Key, relKind string) []Resource {
	i.mu.RLock()
	defer i.mu.RUnlock()
	n := i.store.incoming(key, relKind)
	out := make([]Resource, 0, len(n))
	for _, r := range n {
		out = append(out, *r)
	}
	return out
}

// Stats returns a snapshot of internal counters.
func (i *Informer) Stats() Stats {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return Stats{
		Resources:          len(i.store.resources),
		Relations:          len(i.store.relations),
		Subscribers:        i.subs.len(),
		WritesTotal:        i.writesTotal.Load(),
		DeltasEmittedTotal: i.deltasEmitted.Load(),
		DeltasDroppedTotal: i.deltasDropped.Load(),
		QueueDepth:         len(i.queue),
		QueueCapacity:      i.queueCap,
	}
}
