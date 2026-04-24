package informer

import (
	"maps"
	"time"
)

// store holds the informer's in-memory state. It is not safe for
// concurrent use on its own; the Informer serializes all mutations
// via its writer goroutine and guards reads with its RWMutex.
type store struct {
	resources map[Key]*Resource
	// byKind lets List[Kind] avoid scanning the full resource map.
	byKind map[string]map[Key]*Resource
	// relations indexed by from→to→kind → Relation.
	// Two reverse indexes (outByKind, inByKind) keep Neighbors/Incoming
	// O(edges-of-node) rather than O(total-edges).
	outByKind map[Key]map[string]map[Key]*Relation
	inByKind  map[Key]map[string]map[Key]*Relation
	// flat table keeps Stats.Relations and full iteration O(1) / O(n).
	relations map[relationTripleKey]*Relation
}

type relationTripleKey struct {
	From Key
	To   Key
	Kind string
}

func newStore() *store {
	return &store{
		resources: make(map[Key]*Resource),
		byKind:    make(map[string]map[Key]*Resource),
		outByKind: make(map[Key]map[string]map[Key]*Relation),
		inByKind:  make(map[Key]map[string]map[Key]*Relation),
		relations: make(map[relationTripleKey]*Relation),
	}
}

// get returns the resource pointer for k or nil if absent.
// Callers hold the informer read lock and must not mutate the returned
// pointer; read methods copy before handing values to callers.
func (s *store) get(k Key) *Resource {
	return s.resources[k]
}

// upsert merges u into the store. If the key is new, a Resource is
// constructed from u with FirstSeen/LastSeen set to now and a
// DeltaAdded is produced. If the key exists, non-zero fields of u
// overwrite the stored fields (Labels and Attrs are merged
// key-by-key, not replaced wholesale — feeders clear a key by setting
// it to empty string explicitly, and clear the whole map via Patch).
//
// The supplied Transition is appended to the resource history ring
// and returned in the produced delta.
func (s *store) upsert(u ResourceUpdate, t Transition, now time.Time) (Delta, bool) {
	key := u.Key()
	existing, had := s.resources[key]
	if !had {
		r := Resource{
			Kind:      u.Kind,
			ID:        u.ID,
			Labels:    copyStringMap(u.Labels),
			Attrs:     copyStringMap(u.Attrs),
			Lifecycle: u.Lifecycle,
			FirstSeen: now,
			LastSeen:  now,
		}
		if r.Labels == nil {
			r.Labels = make(map[string]string)
		}
		if r.Attrs == nil {
			r.Attrs = make(map[string]string)
		}
		r.History = appendRing(nil, t)
		stored := r
		s.resources[key] = &stored
		s.indexAdd(&stored)
		return Delta{
			Kind:       DeltaAdded,
			After:      copyResource(&stored),
			Transition: t,
		}, true
	}

	before := copyResource(existing)
	mergeUpdateInto(existing, u, now)
	existing.History = appendRing(existing.History, t)
	return Delta{
		Kind:       DeltaUpdated,
		Before:     before,
		After:      copyResource(existing),
		Transition: t,
	}, true
}

// patch applies fn to the stored resource under key. The caller must
// not retain the pointer passed to fn; all mutation happens while the
// writer holds exclusive access. Returns the Delta and whether the
// key was present.
func (s *store) patch(key Key, fn func(*Resource), t Transition, now time.Time) (Delta, bool) {
	existing, had := s.resources[key]
	if !had {
		return Delta{}, false
	}
	before := copyResource(existing)
	fn(existing)
	// Re-anchor identity — fn must not change Kind or ID. If it does,
	// we overwrite back to preserve map invariants.
	existing.Kind = key.Kind
	existing.ID = key.ID
	existing.LastSeen = now
	existing.History = appendRing(existing.History, t)
	return Delta{
		Kind:       DeltaUpdated,
		Before:     before,
		After:      copyResource(existing),
		Transition: t,
	}, true
}

// remove is a soft-delete: Lifecycle is set to LifecycleGone, the
// transition is appended, and a DeltaRemoved is produced. The
// resource stays in the store with its final state and history for
// forensic reads. A future sweeper may evict LifecycleGone resources
// on a TTL; v1 retains forever.
func (s *store) remove(key Key, t Transition, now time.Time) (Delta, bool) {
	existing, had := s.resources[key]
	if !had {
		return Delta{}, false
	}
	if existing.Lifecycle == LifecycleGone {
		// Idempotent; still append transition so history records the
		// duplicate observation, but don't emit a second DeltaRemoved.
		existing.History = appendRing(existing.History, t)
		existing.LastSeen = now
		return Delta{}, false
	}
	before := copyResource(existing)
	existing.Lifecycle = LifecycleGone
	existing.LastSeen = now
	existing.History = appendRing(existing.History, t)
	return Delta{
		Kind:       DeltaRemoved,
		Before:     before,
		After:      copyResource(existing),
		Transition: t,
	}, true
}

// linkRelation inserts or refreshes a directed edge. Returns whether
// the edge was newly created.
func (s *store) linkRelation(rel Relation, now time.Time) (Delta, bool) {
	tk := relationTripleKey{From: rel.From, To: rel.To, Kind: rel.Kind}
	existing, had := s.relations[tk]
	if had {
		existing.LastSeen = now
		if rel.Attrs != nil {
			if existing.Attrs == nil {
				existing.Attrs = make(map[string]string)
			}
			maps.Copy(existing.Attrs, rel.Attrs)
		}
		return Delta{}, false
	}
	rel.FirstSeen = now
	rel.LastSeen = now
	rel.Attrs = copyStringMap(rel.Attrs)
	if rel.Attrs == nil {
		rel.Attrs = make(map[string]string)
	}
	stored := rel
	s.relations[tk] = &stored
	s.edgeAdd(&stored)
	return Delta{
		Kind:     DeltaRelationAdded,
		relation: copyRelation(&stored),
	}, true
}

// unlinkRelation removes a directed edge. Returns whether one existed.
func (s *store) unlinkRelation(from, to Key, kind string) (Delta, bool) {
	tk := relationTripleKey{From: from, To: to, Kind: kind}
	existing, had := s.relations[tk]
	if !had {
		return Delta{}, false
	}
	delete(s.relations, tk)
	s.edgeRemove(existing)
	return Delta{
		Kind:     DeltaRelationRemoved,
		relation: copyRelation(existing),
	}, true
}

// neighbors lists outbound edges of from with matching kind. An empty
// kind matches every relation kind.
func (s *store) neighbors(from Key, kind string) []*Resource {
	bucket := s.outByKind[from]
	if bucket == nil {
		return nil
	}
	var out []*Resource
	collect := func(m map[Key]*Relation) {
		for _, rel := range m {
			if r := s.resources[rel.To]; r != nil {
				out = append(out, copyResource(r))
			}
		}
	}
	if kind == "" {
		for _, m := range bucket {
			collect(m)
		}
		return out
	}
	collect(bucket[kind])
	return out
}

// incoming lists inbound edges of to with matching kind.
func (s *store) incoming(to Key, kind string) []*Resource {
	bucket := s.inByKind[to]
	if bucket == nil {
		return nil
	}
	var out []*Resource
	collect := func(m map[Key]*Relation) {
		for _, rel := range m {
			if r := s.resources[rel.From]; r != nil {
				out = append(out, copyResource(r))
			}
		}
	}
	if kind == "" {
		for _, m := range bucket {
			collect(m)
		}
		return out
	}
	collect(bucket[kind])
	return out
}

// indexAdd inserts r into every secondary index.
func (s *store) indexAdd(r *Resource) {
	m, ok := s.byKind[r.Kind]
	if !ok {
		m = make(map[Key]*Resource)
		s.byKind[r.Kind] = m
	}
	m[r.Key()] = r
}

// edgeAdd registers rel in outByKind and inByKind.
func (s *store) edgeAdd(rel *Relation) {
	addEdge(s.outByKind, rel.From, rel.Kind, rel.To, rel)
	addEdge(s.inByKind, rel.To, rel.Kind, rel.From, rel)
}

// edgeRemove deregisters rel from outByKind and inByKind.
func (s *store) edgeRemove(rel *Relation) {
	removeEdge(s.outByKind, rel.From, rel.Kind, rel.To)
	removeEdge(s.inByKind, rel.To, rel.Kind, rel.From)
}

func addEdge(idx map[Key]map[string]map[Key]*Relation, a Key, kind string, b Key, rel *Relation) {
	byKind, ok := idx[a]
	if !ok {
		byKind = make(map[string]map[Key]*Relation)
		idx[a] = byKind
	}
	byPeer, ok := byKind[kind]
	if !ok {
		byPeer = make(map[Key]*Relation)
		byKind[kind] = byPeer
	}
	byPeer[b] = rel
}

func removeEdge(idx map[Key]map[string]map[Key]*Relation, a Key, kind string, b Key) {
	byKind := idx[a]
	if byKind == nil {
		return
	}
	byPeer := byKind[kind]
	if byPeer == nil {
		return
	}
	delete(byPeer, b)
	if len(byPeer) == 0 {
		delete(byKind, kind)
	}
	if len(byKind) == 0 {
		delete(idx, a)
	}
}

// appendRing appends t to a ring-buffer history, discarding the oldest
// entry once the ring exceeds historyRingSize.
func appendRing(ring []Transition, t Transition) []Transition {
	if len(ring) < historyRingSize {
		return append(ring, t)
	}
	// Shift-left by one, put t at tail. Allocates once per overflow
	// write; fine for v1.
	out := make([]Transition, historyRingSize)
	copy(out, ring[1:])
	out[historyRingSize-1] = t
	return out
}

// mergeUpdateInto applies u onto dst, preserving dst's identity,
// FirstSeen, and History while refreshing LastSeen. Non-empty
// scalar fields overwrite; Labels/Attrs merge key-by-key.
func mergeUpdateInto(dst *Resource, u ResourceUpdate, now time.Time) {
	if u.Lifecycle != "" {
		dst.Lifecycle = u.Lifecycle
	}
	if u.Labels != nil {
		if dst.Labels == nil {
			dst.Labels = make(map[string]string)
		}
		maps.Copy(dst.Labels, u.Labels)
	}
	if u.Attrs != nil {
		if dst.Attrs == nil {
			dst.Attrs = make(map[string]string)
		}
		maps.Copy(dst.Attrs, u.Attrs)
	}
	dst.LastSeen = now
}

// copyResource returns a deep copy safe to hand to external callers.
func copyResource(r *Resource) *Resource {
	if r == nil {
		return nil
	}
	out := *r
	out.Labels = copyStringMap(r.Labels)
	out.Attrs = copyStringMap(r.Attrs)
	if len(r.History) > 0 {
		out.History = make([]Transition, len(r.History))
		for i, t := range r.History {
			t.Attrs = copyStringMap(t.Attrs)
			out.History[i] = t
		}
	}
	return &out
}

// copyRelation returns a deep copy of rel.
func copyRelation(rel *Relation) Relation {
	if rel == nil {
		return Relation{}
	}
	out := *rel
	out.Attrs = copyStringMap(rel.Attrs)
	return out
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
