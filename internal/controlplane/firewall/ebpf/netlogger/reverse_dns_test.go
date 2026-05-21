package netlogger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestReverseDNSMap_LookupBeforeRefreshReturnsEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, nil)
	if got := m.Lookup(0xdeadbeef); got != "" {
		t.Fatalf("Lookup with no refresh = %q; want empty", got)
	}
}

func TestReverseDNSMap_LookupZeroHashIsAlwaysEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		// Adversarial: try to write the zero hash. refresh must
		// drop it; this guard is what keeps direct-IP connects
		// (DomainHash=0 on the BPF side) from accidentally hitting
		// a stale entry.
		visit(0)
		return nil
	}, nil)
	m.refresh()
	if got := m.Lookup(0); got != "" {
		t.Fatalf("Lookup(0) = %q; want empty", got)
	}
}

func TestReverseDNSMap_RefreshObservesHashes(t *testing.T) {
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		visit(1)
		visit(2)
		visit(3)
		return nil
	}, nil)
	m.refresh()
	// v1 limitation: Lookup returns "" because there is no domain
	// string source yet. But the hashes are recorded in byHash.
	// Assert via the private field — that's the contract the
	// follow-up branch builds on.
	if _, ok := m.byHash[1]; !ok {
		t.Fatalf("expected hash 1 to be observed")
	}
	if got := m.Lookup(1); got != "" {
		t.Fatalf("Lookup(1) = %q; v1 must return empty until follow-up populates strings", got)
	}
}

func TestReverseDNSMap_RefreshIsAtomicSwap(t *testing.T) {
	// Two refresh passes — the second sees a different set. The map
	// must reflect ONLY the latest set; refresh replaces, not merges.
	round := atomic.Int32{}
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		if round.Add(1) == 1 {
			visit(1)
			visit(2)
		} else {
			visit(3)
		}
		return nil
	}, nil)
	m.refresh()
	m.refresh()
	if _, ok := m.byHash[1]; ok {
		t.Fatalf("byHash[1] should have been dropped by the second refresh")
	}
	if _, ok := m.byHash[3]; !ok {
		t.Fatalf("byHash[3] should have been added by the second refresh")
	}
}

func TestReverseDNSMap_RefreshErrorKeepsLastGoodState(t *testing.T) {
	// First refresh succeeds; second errors. The map must keep the
	// previously observed set so an iteration glitch doesn't blank
	// downstream domain attribution.
	round := atomic.Int32{}
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		if round.Add(1) == 1 {
			visit(42)
			return nil
		}
		return errors.New("simulated dns_cache iterate failure")
	}, nil)
	m.refresh()
	m.refresh()
	if _, ok := m.byHash[42]; !ok {
		t.Fatalf("refresh error wiped the previous map; want last-good state retained")
	}
}

func TestReverseDNSMap_Run_PopulatesImmediatelyAndOnTick(t *testing.T) {
	calls := atomic.Int32{}
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		calls.Add(1)
		visit(uint32(calls.Load()))
		return nil
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.Run(ctx, 25*time.Millisecond)
		close(done)
	}()
	// Wait long enough for ≥2 ticks beyond the immediate refresh.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Run did not exit after ctx cancel")
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("expected ≥2 refresh calls (immediate + tick); got %d", got)
	}
}
