package netlogger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

func TestReverseDNSMap_LookupBeforeRefreshReturnsEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(func(func(clawkerebpf.RouteIdentity)) error { return nil }, nil, nil)
	if got := m.Lookup(0xdeadbeef); got != "" {
		t.Fatalf("Lookup with no refresh = %q; want empty", got)
	}
}

func TestReverseDNSMap_LookupZeroIdentityIsAlwaysEmpty(t *testing.T) {
	m := NewReverseDNSMapWithWalk(
		func(func(clawkerebpf.RouteIdentity)) error { return nil },
		func() map[clawkerebpf.RouteIdentity]string {
			return map[clawkerebpf.RouteIdentity]string{300: "example.com"}
		},
		nil,
	)
	m.refresh()
	if got := m.Lookup(0); got != "" {
		t.Fatalf("Lookup(0) = %q; want empty", got)
	}
}

func TestReverseDNSMap_RefreshPopulatesFromIdentitySource(t *testing.T) {
	identities := func() map[clawkerebpf.RouteIdentity]string {
		return map[clawkerebpf.RouteIdentity]string{261: "github.com", 262: "example.com"}
	}
	m := NewReverseDNSMapWithWalk(func(func(clawkerebpf.RouteIdentity)) error { return nil }, identities, nil)
	m.refresh()
	if got := m.Lookup(261); got != "github.com" {
		t.Fatalf("Lookup(261) = %q; want github.com", got)
	}
	if got := m.Lookup(262); got != "example.com" {
		t.Fatalf("Lookup(262) = %q; want example.com", got)
	}
}

func TestReverseDNSMap_RefreshIsAtomicSwap(t *testing.T) {
	// Two refresh passes — the second sees a different table. The map
	// must reflect ONLY the latest table; refresh replaces, not merges.
	round := atomic.Int32{}
	identities := func() map[clawkerebpf.RouteIdentity]string {
		if round.Add(1) == 1 {
			return map[clawkerebpf.RouteIdentity]string{256: "alpha.example", 257: "beta.example"}
		}
		return map[clawkerebpf.RouteIdentity]string{258: "gamma.example"}
	}
	m := NewReverseDNSMapWithWalk(func(func(clawkerebpf.RouteIdentity)) error { return nil }, identities, nil)
	m.refresh()
	m.refresh()
	if got := m.Lookup(256); got != "" {
		t.Fatalf("alpha.example should have been dropped by the second refresh; got %q", got)
	}
	if got := m.Lookup(258); got != "gamma.example" {
		t.Fatalf("gamma.example should be present after the second refresh; got %q", got)
	}
}

func TestReverseDNSMap_WalkErrorDoesNotBlankAttribution(t *testing.T) {
	// IdentitySource is the source of truth for byID; a dns_cache
	// iterate failure must not wipe attribution. The walk loop runs
	// only for the unattributed-identity diagnostic.
	identities := func() map[clawkerebpf.RouteIdentity]string {
		return map[clawkerebpf.RouteIdentity]string{261: "github.com"}
	}
	m := NewReverseDNSMapWithWalk(
		func(func(clawkerebpf.RouteIdentity)) error { return errors.New("simulated dns_cache iterate failure") },
		identities,
		nil,
	)
	m.refresh()
	if got := m.Lookup(261); got != "github.com" {
		t.Fatalf("walk error wiped IdentitySource attribution; got %q", got)
	}
}

func TestReverseDNSMap_NilIdentitySourceLeavesByIDEmpty(t *testing.T) {
	// Degraded mode: no IdentitySource wired. refresh runs cleanly,
	// every Lookup returns "" — same shape as boot-time before the
	// CP main wiring lands.
	m := NewReverseDNSMapWithWalk(func(visit func(clawkerebpf.RouteIdentity)) error {
		visit(0xdeadbeef)
		return nil
	}, nil, nil)
	m.refresh()
	if got := m.Lookup(0xdeadbeef); got != "" {
		t.Fatalf("Lookup with nil IdentitySource = %q; want empty", got)
	}
}

func TestReverseDNSMap_Run_PopulatesImmediatelyAndOnTick(t *testing.T) {
	calls := atomic.Int32{}
	identities := func() map[clawkerebpf.RouteIdentity]string {
		calls.Add(1)
		return map[clawkerebpf.RouteIdentity]string{261: "github.com"}
	}
	m := NewReverseDNSMapWithWalk(func(func(clawkerebpf.RouteIdentity)) error { return nil }, identities, nil)
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
		t.Fatalf("expected ≥2 IdentitySource calls (immediate + tick); got %d", got)
	}
}
