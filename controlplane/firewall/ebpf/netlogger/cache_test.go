package netlogger

import (
	"fmt"
	"sync"
	"testing"
)

func TestLabelCache_Lookup_HitAndMiss(t *testing.T) {
	c := NewLabelCache(nil)
	c.AddOrUpdate(42, "abc", "agent-a", "proj")
	cid, agent, proj, ok := c.Lookup(42)
	if !ok || cid != "abc" || agent != "agent-a" || proj != "proj" {
		t.Fatalf("hit: got cid=%q agent=%q proj=%q ok=%v", cid, agent, proj, ok)
	}
	if _, _, _, ok := c.Lookup(99); ok {
		t.Fatalf("miss: expected ok=false on absent cgroup_id")
	}
}

// TestLabelCache_AddOrUpdate_ReEnroll covers the "same containerID
// re-binds" branch: a second AddOrUpdate must update labels in place
// without growing the entry count. Catches a regression where the
// idempotent path forgot to find the existing entry.
func TestLabelCache_AddOrUpdate_ReEnroll(t *testing.T) {
	c := NewLabelCache(nil)
	c.AddOrUpdate(42, "abc", "old-name", "proj")
	c.AddOrUpdate(42, "abc", "new-name", "proj")
	if got := c.Len(); got != 1 {
		t.Fatalf("Len after re-enroll = %d; want 1", got)
	}
	_, agent, _, _ := c.Lookup(42)
	if agent != "new-name" {
		t.Fatalf("agent = %q; want %q (refresh)", agent, "new-name")
	}
}

func TestLabelCache_EvictByContainerID(t *testing.T) {
	c := NewLabelCache(nil)
	c.AddOrUpdate(42, "abc", "agent-a", "proj")
	c.EvictByContainerID("abc")
	if _, _, _, ok := c.Lookup(42); ok {
		t.Fatalf("expected Lookup miss after evict")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len after evict = %d; want 0", got)
	}
}

func TestLabelCache_EvictByContainerID_UnknownIsNoop(t *testing.T) {
	c := NewLabelCache(nil)
	c.EvictByContainerID("ghost") // must not panic
}

// TestLabelCache_CgroupIDReuse is the load-bearing test: the kernel
// reuses a cgroup_id after the prior cgroup's destroy. We model the
// scenario where the previous container's evict landed (so EvictByContainerID
// cleared both index entries) and the next enroll arrives on the SAME
// cgroup_id under a DIFFERENT container. Lookup must return the NEW
// container's labels, never the dead one's.
func TestLabelCache_CgroupIDReuse(t *testing.T) {
	c := NewLabelCache(nil)

	c.AddOrUpdate(42, "abc", "agent-a", "proj")
	c.EvictByContainerID("abc")
	c.AddOrUpdate(42, "xyz", "agent-b", "proj")

	cid, agent, _, ok := c.Lookup(42)
	if !ok {
		t.Fatalf("Lookup(42) = miss; want hit on reused cgroup_id")
	}
	if cid != "xyz" || agent != "agent-b" {
		t.Fatalf("got cid=%q agent=%q; want xyz/agent-b", cid, agent)
	}
}

// TestLabelCache_CgroupIDReuse_MissedEvict guards the defensive
// path: cgroup_id arrives for a new container WITHOUT a prior evict
// for the old occupant. The cache overwrites in place and the old
// container_id index is dropped so a late evict for it is a no-op.
func TestLabelCache_CgroupIDReuse_MissedEvict(t *testing.T) {
	c := NewLabelCache(nil)
	c.AddOrUpdate(42, "abc", "agent-a", "proj")
	c.AddOrUpdate(42, "xyz", "agent-b", "proj") // missed evict for abc

	cid, _, _, _ := c.Lookup(42)
	if cid != "xyz" {
		t.Fatalf("after missed-evict overwrite, Lookup(42) = %q; want xyz", cid)
	}
	// Late evict for the displaced container is a no-op.
	c.EvictByContainerID("abc")
	cid, _, _, _ = c.Lookup(42)
	if cid != "xyz" {
		t.Fatalf("late stale evict broke fresh binding: Lookup(42) = %q", cid)
	}
}

func TestLabelCache_AddOrUpdate_EmptyContainerIDIsRejected(t *testing.T) {
	c := NewLabelCache(nil)
	c.AddOrUpdate(42, "", "agent-a", "proj")
	if _, _, _, ok := c.Lookup(42); ok {
		t.Fatalf("expected empty containerID to be rejected")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len = %d; want 0", got)
	}
}

// TestLabelCache_SlotRecycling pins the free-list path: evicting and
// re-adding many containers should not grow entries unboundedly.
func TestLabelCache_SlotRecycling(t *testing.T) {
	c := NewLabelCache(nil)
	const iters = 64
	for i := 0; i < iters; i++ {
		cid := fmt.Sprintf("cont-%d", i)
		c.AddOrUpdate(uint64(i+1), cid, "agent", "proj")
		c.EvictByContainerID(cid)
	}
	if got := len(c.entries); got > iters {
		t.Fatalf("entries grew to %d after %d add+evict cycles; expected slot recycling", got, iters)
	}
}

// TestLabelCache_Concurrent stresses the mutex contract under -race.
// Three goroutines: writers (AddOrUpdate), evictors (EvictByContainerID),
// readers (Lookup). The assertion is just "no race detector trips and
// the cache survives intact" — the test isn't trying to pin a specific
// final state because the schedule is non-deterministic.
func TestLabelCache_Concurrent(t *testing.T) {
	c := NewLabelCache(nil)
	const iters = 200
	const workers = 8

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {

		wg.Add(3)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				cid := fmt.Sprintf("c-%d-%d", w, i)
				c.AddOrUpdate(uint64(w*iters+i+1), cid, "agent", "proj")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				cid := fmt.Sprintf("c-%d-%d", w, i)
				c.EvictByContainerID(cid)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _, _, _ = c.Lookup(uint64(w*iters + i + 1))
			}
		}()
	}
	wg.Wait()
}
