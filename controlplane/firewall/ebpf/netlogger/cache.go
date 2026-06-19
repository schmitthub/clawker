package netlogger

import (
	"sync"

	"github.com/schmitthub/clawker/internal/logger"
)

// LabelCache resolves a kernel-attested cgroup_id to the userspace
// {container_id, agent, project} attribution the OTel sink ships on
// every emitted record. Hydrated by EBPFContainerEnrolled events from
// the overseer bus (one Docker ContainerInspect per enroll); evicted
// by container die/destroy events on the same bus.
//
// Storage shape: a free-list-backed slice of entries plus two index
// maps (by cgroup_id, by container_id). The dual index is the
// load-bearing piece — die/destroy events arrive keyed by
// container_id, but lookups are keyed by cgroup_id, and the kernel
// reuses cgroup_id values once a cgroup is destroyed. Eviction MUST
// mark the entry invalid AND drop the cgroup_id → entry mapping in
// the same critical section, so the next AddOrUpdate against a
// reused cgroup_id binds to the new container's labels without
// returning the dead container's identity in the gap.
//
// Why not sync.Map: the eviction step needs an atomic "find by
// container_id AND drop the cgroup_id index entry pointing at the
// same record" — two-key atomic operations are exactly what a
// single mutex makes trivial.
//
// Why not an LRU: cgroup_id reuse is event-driven, not time-driven.
// Aging entries out on a wall clock would either be too aggressive
// (evicting a live container's binding mid-traffic) or too slow
// (handing back the previous tenant's labels for cgroup_id that the
// kernel has already reassigned).
type LabelCache struct {
	mu       sync.Mutex
	entries  []labelEntry
	free     []int          // recycled slot indices
	byCgroup map[uint64]int // cgroup_id -> entries idx
	byCont   map[string]int // container_id -> entries idx
	log      *logger.Logger
}

type labelEntry struct {
	cgroupID    uint64
	containerID string
	agent       string
	project     string
	invalid     bool
}

// NewLabelCache constructs an empty LabelCache. The logger is used
// for diagnostic lines (cgroup-id reuse warnings, eviction events);
// nil resolves to a no-op logger.
func NewLabelCache(log *logger.Logger) *LabelCache {
	if log == nil {
		log = logger.Nop()
	}
	return &LabelCache{
		byCgroup: make(map[uint64]int),
		byCont:   make(map[string]int),
		log:      log,
	}
}

// Lookup returns the labels bound to cgroupID, or zero values + false
// if the cache has no live entry. An entry marked invalid by
// EvictByContainerID is treated as absent — the cgroup_id index
// gets removed at eviction time, so this branch is mostly defensive.
func (c *LabelCache) Lookup(cgroupID uint64) (containerID, agent, project string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, found := c.byCgroup[cgroupID]
	if !found {
		return "", "", "", false
	}
	e := c.entries[idx]
	if e.invalid {
		return "", "", "", false
	}
	return e.containerID, e.agent, e.project, true
}

// AddOrUpdate binds a cgroup_id to container labels. Idempotent
// re-call with the same containerID updates the labels in place. A
// re-call for the SAME cgroup_id under a DIFFERENT containerID is
// the kernel-cgroup-id-reuse case (the prior container's
// EvictByContainerID landed before this enroll fired) — the old
// entry's slot gets reused.
//
// Pre-condition: containerID must be non-empty. Empty container IDs
// would collide in byCont (multiple "" entries pointing at one
// slot) — the caller is the enroll-event handler which always has a
// concrete container ID; an empty value indicates a wiring bug
// upstream and the call is a no-op.
func (c *LabelCache) AddOrUpdate(cgroupID uint64, containerID, agent, project string) {
	if containerID == "" {
		c.log.Warn().
			Uint64("cgroup_id", cgroupID).
			Str("event", "netlogger_labelcache_empty_container_id").
			Msg("LabelCache.AddOrUpdate: empty containerID; ignoring")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Same container, same cgroup_id — refresh labels in place.
	if idx, ok := c.byCont[containerID]; ok {
		// Bring the entry back from invalid (a fresh enroll
		// dispels the previous evict).
		entry := &c.entries[idx]
		// If the container was handed a different cgroup_id since
		// the last bind (uncommon within a single docker container
		// lifetime), drop the stale byCgroup index. We know the
		// prior cgroup directly from the entry — no scan.
		if entry.cgroupID != cgroupID {
			delete(c.byCgroup, entry.cgroupID)
		}
		entry.cgroupID = cgroupID
		entry.agent = agent
		entry.project = project
		entry.invalid = false
		c.byCgroup[cgroupID] = idx
		return
	}

	// New container. If the cgroup_id is already mapped, the prior
	// owner missed its evict — invalidate it in place so Lookup
	// can't return its labels, then reuse the slot.
	if idx, ok := c.byCgroup[cgroupID]; ok {
		prior := c.entries[idx]
		c.log.Warn().
			Uint64("cgroup_id", cgroupID).
			Str("prior_container_id", prior.containerID).
			Str("new_container_id", containerID).
			Str("event", "netlogger_labelcache_cgroup_reuse").
			Msg("LabelCache: cgroup_id reused before prior container evicted")
		delete(c.byCont, prior.containerID)
		c.entries[idx] = labelEntry{
			cgroupID:    cgroupID,
			containerID: containerID,
			agent:       agent,
			project:     project,
		}
		c.byCont[containerID] = idx
		return
	}

	// Fresh entry. Pop a slot from the free list, or grow the
	// entries slice.
	var idx int
	if n := len(c.free); n > 0 {
		idx = c.free[n-1]
		c.free = c.free[:n-1]
		c.entries[idx] = labelEntry{
			cgroupID:    cgroupID,
			containerID: containerID,
			agent:       agent,
			project:     project,
		}
	} else {
		idx = len(c.entries)
		c.entries = append(c.entries, labelEntry{
			cgroupID:    cgroupID,
			containerID: containerID,
			agent:       agent,
			project:     project,
		})
	}
	c.byCgroup[cgroupID] = idx
	c.byCont[containerID] = idx
}

// EvictByContainerID removes the binding for a container_id. Called
// when a dockerevents.DockerEvent reports die/destroy for the
// container. Marks the entry invalid, drops both index entries, and
// recycles the slot. The invalid flag is defensive: if a lookup
// races with eviction (very rare — the dispatch is serialised on
// the bus loop but the lookup runs on the processor goroutine), the
// flag short-circuits the return rather than handing back a
// container_id about to disappear.
//
// Idempotent: a no-op when the container_id is not in the cache.
func (c *LabelCache) EvictByContainerID(containerID string) {
	if containerID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, ok := c.byCont[containerID]
	if !ok {
		return
	}
	entry := c.entries[idx]
	c.entries[idx] = labelEntry{invalid: true}
	delete(c.byCont, containerID)
	delete(c.byCgroup, entry.cgroupID)
	c.free = append(c.free, idx)
}

// Len returns the live entry count.
func (c *LabelCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byCont)
}
