package netlogger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	clawkerebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
)

func TestReverseDNSMap_LookupBeforeRefreshReturnsEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, nil, nil)
	if got := m.Lookup(0xdeadbeef); got != "" {
		t.Fatalf("Lookup with no refresh = %q; want empty", got)
	}
}

func TestReverseDNSMap_LookupZeroHashIsAlwaysEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(
		func(func(uint32)) error { return nil },
		func() []string { return []string{"example.com"} },
		nil,
	)
	m.refresh()
	if got := m.Lookup(0); got != "" {
		t.Fatalf("Lookup(0) = %q; want empty", got)
	}
}

func TestReverseDNSMap_RefreshPopulatesFromDomainSource(t *testing.T) {
	domains := func() []string { return []string{"github.com", "example.com"} }
	m := NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, domains, nil)
	m.refresh()
	if got := m.Lookup(clawkerebpf.DomainHash("github.com")); got != "github.com" {
		t.Fatalf("Lookup(hash(github.com)) = %q; want github.com", got)
	}
	if got := m.Lookup(clawkerebpf.DomainHash("example.com")); got != "example.com" {
		t.Fatalf("Lookup(hash(example.com)) = %q; want example.com", got)
	}
}

func TestReverseDNSMap_RefreshIsAtomicSwap(t *testing.T) {
	// Two refresh passes — the second sees a different set. The map
	// must reflect ONLY the latest set; refresh replaces, not merges.
	round := atomic.Int32{}
	domains := func() []string {
		if round.Add(1) == 1 {
			return []string{"alpha.example", "beta.example"}
		}
		return []string{"gamma.example"}
	}
	m := NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, domains, nil)
	m.refresh()
	m.refresh()
	if got := m.Lookup(clawkerebpf.DomainHash("alpha.example")); got != "" {
		t.Fatalf("alpha.example should have been dropped by the second refresh; got %q", got)
	}
	if got := m.Lookup(clawkerebpf.DomainHash("gamma.example")); got != "gamma.example" {
		t.Fatalf("gamma.example should be present after the second refresh; got %q", got)
	}
}

func TestReverseDNSMap_WalkErrorDoesNotBlankAttribution(t *testing.T) {
	// DomainSource is the source of truth for byHash; a dns_cache
	// iterate failure must not wipe attribution. The walk loop runs
	// only for the unattributed-hash diagnostic.
	domains := func() []string { return []string{"github.com"} }
	m := NewReverseDNSMapWithWalk(
		func(func(uint32)) error { return errors.New("simulated dns_cache iterate failure") },
		domains,
		nil,
	)
	m.refresh()
	if got := m.Lookup(clawkerebpf.DomainHash("github.com")); got != "github.com" {
		t.Fatalf("walk error wiped DomainSource attribution; got %q", got)
	}
}

func TestReverseDNSMap_NilDomainSourceLeavesByHashEmpty(t *testing.T) {
	// Degraded mode: no DomainSource wired. refresh runs cleanly,
	// every Lookup returns "" — same shape as boot-time before the
	// CP main wiring lands.
	m := NewReverseDNSMapWithWalk(func(visit func(uint32)) error {
		visit(0xdeadbeef)
		return nil
	}, nil, nil)
	m.refresh()
	if got := m.Lookup(0xdeadbeef); got != "" {
		t.Fatalf("Lookup with nil DomainSource = %q; want empty", got)
	}
}

func TestReverseDNSMap_Run_PopulatesImmediatelyAndOnTick(t *testing.T) {
	calls := atomic.Int32{}
	domains := func() []string {
		calls.Add(1)
		return []string{"github.com"}
	}
	m := NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, domains, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.Run(ctx, 25*time.Millisecond)
		close(done)
	}()
	// Wait long enough for ≥2 calls beyond the immediate refresh.
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
		t.Fatalf("expected ≥2 DomainSource calls (immediate + tick); got %d", got)
	}
}
