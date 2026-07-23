package firewall

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// allocRules builds one https allow rule per dst — the minimal rule shape
// SyncFromRules consumes.
func allocRules(dsts ...string) []config.EgressRule {
	rules := make([]config.EgressRule, 0, len(dsts))
	for _, d := range dsts {
		rules = append(rules, config.EgressRule{
			Dst: d, Proto: "https", Action: "allow",
			Port: "", PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
		})
	}
	return rules
}

func newTestAllocator(t *testing.T) *IdentityAllocator {
	t.Helper()
	return newTestAllocatorWithCfg(t, configmocks.NewIsolatedTestConfig(t))
}

func newTestAllocatorWithCfg(t *testing.T, cfg config.Config) *IdentityAllocator {
	t.Helper()
	store, err := NewIdentityStore(cfg)
	require.NoError(t, err)
	a, err := NewIdentityAllocator(store)
	require.NoError(t, err)
	return a
}

// Identities start above the reserved band and zero is never allocated.
func TestIdentityAllocator_AllocatesAboveReservedBand(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncFromRules(allocRules("github.com", "gitlab.com")))

	for _, dst := range []string{"github.com", "gitlab.com"} {
		id, ok := a.IdentityFor(dst)
		require.True(t, ok, dst)
		assert.GreaterOrEqual(t, id, MinIdentity, "identity must be outside the reserved band")
		assert.NotZero(t, id)
	}
}

// THE regression test for the rejected deterministic-allocation design:
// live identities must never change value across arbitrary rule churn.
// dns_cache is pinned and populated asynchronously — a renumbered identity
// silently cross-routes cached IPs to another domain's listener.
func TestIdentityAllocator_StickyAcrossChurn(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncFromRules(allocRules("github.com", "gitlab.com", "pypi.org")))

	pin := map[string]uint32{}
	for _, dst := range []string{"github.com", "gitlab.com", "pypi.org"} {
		id, ok := a.IdentityFor(dst)
		require.True(t, ok)
		pin[dst] = id
	}

	// Churn: add sort-order-shifting dsts, remove others, re-add, repeat.
	churn := [][]string{
		{"aaa.dev", "github.com", "gitlab.com", "pypi.org"},                // prepend in sort order
		{"aaa.dev", "github.com", "pypi.org"},                              // drop gitlab
		{"aaa.dev", "github.com", "gitlab.com", "pypi.org", "zzz.io"},      // re-add + append
		{"github.com", "gitlab.com", "pypi.org"},                           // back to start
		{"000.example", "github.com", "gitlab.com", "mmm.net", "pypi.org"}, // interleave
	}
	// Stickiness applies to continuously live dsts. A released dst that
	// returns later gets a FRESH identity by design (cilium semantics:
	// release forgets; stale pinned dns_cache entries under the old
	// identity must fail closed, not re-validate on re-add).
	for i, set := range churn {
		require.NoError(t, a.SyncFromRules(allocRules(set...)), "churn step %d", i)
		next := map[string]uint32{}
		for _, dst := range set {
			got, ok := a.IdentityFor(dst)
			require.True(t, ok, "churn step %d: %s has no identity", i, dst)
			if want, wasLive := pin[dst]; wasLive {
				assert.Equal(t, want, got, "churn step %d: %s was renumbered while live", i, dst)
			}
			next[dst] = got
		}
		pin = next
	}
}

// A released identity is not handed to the next allocation (round-robin
// next-free advances past it), so pinned dns_cache entries holding the
// dead identity cannot alias a newly added domain within TTL windows.
func TestIdentityAllocator_NoImmediateReuseAfterRelease(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncFromRules(allocRules("github.com", "gitlab.com")))
	released, ok := a.IdentityFor("gitlab.com")
	require.True(t, ok)

	require.NoError(t, a.SyncFromRules(allocRules("github.com")))
	_, ok = a.IdentityFor("gitlab.com")
	require.False(t, ok, "released dst must not resolve")

	require.NoError(t, a.SyncFromRules(allocRules("github.com", "newcomer.dev")))
	fresh, ok := a.IdentityFor("newcomer.dev")
	require.True(t, ok)
	assert.NotEqual(t, released, fresh, "freed identity reissued immediately")
}

// Restart stability: a new allocator over the same store must return the
// identical table and continue allocating without colliding with it.
func TestIdentityAllocator_PersistenceRoundTrip(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	a := newTestAllocatorWithCfg(t, cfg)
	require.NoError(t, a.SyncFromRules(allocRules("github.com", "gitlab.com", "pypi.org")))
	want := a.Snapshot()
	require.Len(t, want, 3)

	store2, err := NewIdentityStore(cfg)
	require.NoError(t, err)
	b, err := NewIdentityAllocator(store2)
	require.NoError(t, err)
	assert.Equal(t, want, b.Snapshot(), "restart must not flap identities")

	// Continue allocating on the restored allocator: no collision with
	// restored IDs.
	require.NoError(t, b.SyncFromRules(allocRules("github.com", "gitlab.com", "pypi.org", "extra.dev")))
	extra, ok := b.IdentityFor("extra.dev")
	require.True(t, ok)
	for dst, id := range want {
		assert.NotEqual(t, id, extra, "new identity collided with restored %s", dst)
	}
}

// Acceptance bar from the initiative: thousands of dsts, all unique.
func TestIdentityAllocator_UniquenessAtScale(t *testing.T) {
	a := newTestAllocator(t)
	dsts := make([]string, 0, 5000)
	for i := range 5000 {
		dsts = append(dsts, fmt.Sprintf("host-%04d.example.com", i))
	}
	require.NoError(t, a.SyncFromRules(allocRules(dsts...)))

	seen := map[uint32]string{}
	for _, dst := range dsts {
		id, ok := a.IdentityFor(dst)
		require.True(t, ok, dst)
		if prev, dup := seen[id]; dup {
			t.Fatalf("identity %d allocated to both %s and %s", id, prev, dst)
		}
		seen[id] = dst
	}
}

// The allocator normalizes dsts exactly like the rest of the firewall
// (normalizeDomain: trim leading wildcard dot + trailing dot; dsts arrive
// pre-validated lowercase), so every caller observes one identity per
// logical destination.
func TestIdentityAllocator_NormalizesDst(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncFromRules(allocRules(".example.com")))

	want, ok := a.IdentityFor("example.com")
	require.True(t, ok, "normalized lookup must resolve")
	for _, alias := range []string{".example.com", "example.com."} {
		got, aliasOK := a.IdentityFor(alias)
		require.True(t, aliasOK, alias)
		assert.Equal(t, want, got, alias)
	}
}

// IP-literal dsts get identities too — they are seeded into dns_cache by
// SyncRoutes and must be attributable like any domain.
func TestIdentityAllocator_IPLiteralDsts(t *testing.T) {
	a := newTestAllocator(t)
	ipRule := config.EgressRule{
		Dst: "203.0.113.7", Proto: "tcp", Port: "4242", Action: "allow",
		PathRules: nil, PathDefault: "", InsecureSkipTLSVerify: false,
	}
	require.NoError(t, a.SyncFromRules(append([]config.EgressRule{ipRule}, allocRules("github.com")...)))
	ipID, ok := a.IdentityFor("203.0.113.7")
	require.True(t, ok)
	domID, ok := a.IdentityFor("github.com")
	require.True(t, ok)
	assert.NotEqual(t, ipID, domID)
}

// DomainFor is the netlogger attribution surface: exact inverse of the
// live table, absent for released or never-allocated identities.
func TestIdentityAllocator_DomainFor(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncFromRules(allocRules("github.com")))
	id, ok := a.IdentityFor("github.com")
	require.True(t, ok)

	dst, ok := a.DomainFor(id)
	require.True(t, ok)
	assert.Equal(t, "github.com", dst)

	_, ok = a.DomainFor(id + 1)
	assert.False(t, ok)

	require.NoError(t, a.SyncFromRules(nil))
	_, ok = a.DomainFor(id)
	assert.False(t, ok, "released identity must not reverse-resolve")
}
