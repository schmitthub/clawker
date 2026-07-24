package firewall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

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

// writeIdentityFile hand-crafts the persisted table so constructor tests can
// exercise shapes the allocator itself never writes.
func writeIdentityFile(t *testing.T, cfg config.Config, content string) {
	t.Helper()
	dataDir, err := cfg.FirewallDataSubdir()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, consts.RouteIdentitiesFile), []byte(content), 0o644))
}

// Identities start above the reserved band and zero is never allocated.
func TestIdentityAllocator_AllocatesAboveReservedBand(t *testing.T) {
	a := newTestAllocator(t)
	require.NoError(t, a.SyncDsts([]string{"github.com", "gitlab.com"}))

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
	require.NoError(t, a.SyncDsts([]string{"github.com", "gitlab.com", "pypi.org"}))

	pin := map[string]ebpf.RouteIdentity{}
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
		require.NoError(t, a.SyncDsts(set), "churn step %d", i)
		next := map[string]ebpf.RouteIdentity{}
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
	require.NoError(t, a.SyncDsts([]string{"github.com", "gitlab.com"}))
	released, ok := a.IdentityFor("gitlab.com")
	require.True(t, ok)

	require.NoError(t, a.SyncDsts([]string{"github.com"}))
	_, ok = a.IdentityFor("gitlab.com")
	require.False(t, ok, "released dst must not resolve")

	require.NoError(t, a.SyncDsts([]string{"github.com", "newcomer.dev"}))
	fresh, ok := a.IdentityFor("newcomer.dev")
	require.True(t, ok)
	assert.NotEqual(t, released, fresh, "freed identity reissued immediately")
}

// Restart stability: a new allocator over the same store must return the
// identical table and continue allocating without colliding with it.
func TestIdentityAllocator_PersistenceRoundTrip(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	a := newTestAllocatorWithCfg(t, cfg)
	require.NoError(t, a.SyncDsts([]string{"github.com", "gitlab.com", "pypi.org"}))
	want := a.Snapshot()
	require.Len(t, want, 3)

	b := newTestAllocatorWithCfg(t, cfg)
	assert.Equal(t, want, b.Snapshot(), "restart must not flap identities")

	// Continue allocating on the restored allocator: no collision with
	// restored IDs.
	require.NoError(t, b.SyncDsts([]string{"github.com", "gitlab.com", "pypi.org", "extra.dev"}))
	extra, ok := b.IdentityFor("extra.dev")
	require.True(t, ok)
	for dst, id := range want {
		assert.NotEqual(t, id, extra, "new identity collided with restored %s", dst)
	}
}

// No-reuse must hold across restarts too: the persisted cursor keeps a
// pre-restart release out of circulation, so a stale pinned dns_cache entry
// cannot alias a dst added after the restart.
func TestIdentityAllocator_NoReuseAcrossRestart(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	a := newTestAllocatorWithCfg(t, cfg)
	require.NoError(t, a.SyncDsts([]string{"github.com", "gitlab.com"}))
	keepID, ok := a.IdentityFor("github.com")
	require.True(t, ok)
	releasedID, ok := a.IdentityFor("gitlab.com")
	require.True(t, ok)

	require.NoError(t, a.SyncDsts([]string{"github.com"})) // release gitlab

	b := newTestAllocatorWithCfg(t, cfg)
	require.NoError(t, b.SyncDsts([]string{"github.com", "newcomer.dev"}))

	got, ok := b.IdentityFor("github.com")
	require.True(t, ok)
	assert.Equal(t, keepID, got, "surviving dst renumbered across restart")

	fresh, ok := b.IdentityFor("newcomer.dev")
	require.True(t, ok)
	assert.NotEqual(t, releasedID, fresh, "identity released before restart reissued after it")
}

// failingOnceStore fails the first Txn it sees, then delegates — the minimal
// persist-failure shape (transient disk error between two syncs).
type failingOnceStore struct {
	inner identityStore
	fail  bool
}

var errInjectedWrite = errors.New("injected write failure")

func (s *failingOnceStore) Txn(fn func(tx *storage.Tx[IdentityTableFile]) error) error {
	if s.fail {
		s.fail = false
		return errInjectedWrite
	}
	return s.inner.Txn(fn)
}

// A failed persist must not be masked by the in-memory maps already holding
// the new table: the next sync — even a no-change one — retries the write, so
// the table reaches disk and a restart cannot renumber live identities.
func TestIdentityAllocator_PersistFailureRetriedOnNextSync(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := NewIdentityStore(cfg)
	require.NoError(t, err)
	a, err := NewIdentityAllocator(store)
	require.NoError(t, err)
	a.store = &failingOnceStore{inner: store, fail: true}

	dsts := []string{"github.com", "gitlab.com"}
	require.ErrorIs(t, a.SyncDsts(dsts), errInjectedWrite)

	// Same dst set: no in-memory change, but the owed persist must retry.
	require.NoError(t, a.SyncDsts(dsts))

	b := newTestAllocatorWithCfg(t, cfg)
	assert.Equal(t, a.Snapshot(), b.Snapshot(), "retried persist did not reach disk")
	assert.Len(t, b.Snapshot(), 2)
}

// A populated table with an out-of-range cursor is corrupt: the cursor is
// what keeps released identities out of circulation, so construction fails
// (startup gate) rather than silently resetting it. An empty table keeps the
// silent MinIdentity default — that is the fresh-file shape.
func TestIdentityAllocator_CursorValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "entries with zero cursor",
			yaml:    "entries:\n    - dst: github.com\n      id: 300\nnext: 0\n",
			wantErr: "cursor 0 out of range",
		},
		{
			name:    "entries with negative cursor",
			yaml:    "entries:\n    - dst: github.com\n      id: 300\nnext: -7\n",
			wantErr: "cursor -7 out of range",
		},
		{
			name:    "entries with cursor above MaxUint32",
			yaml:    "entries:\n    - dst: github.com\n      id: 300\nnext: 4294967296\n",
			wantErr: "cursor 4294967296 out of range",
		},
		{
			name:    "empty table with zero cursor",
			yaml:    "entries: []\nnext: 0\n",
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := configmocks.NewIsolatedTestConfig(t)
			writeIdentityFile(t, cfg, tt.yaml)
			store, err := NewIdentityStore(cfg)
			require.NoError(t, err)
			a, err := NewIdentityAllocator(store)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NoError(t, a.SyncDsts([]string{"fresh.dev"}))
			id, ok := a.IdentityFor("fresh.dev")
			require.True(t, ok)
			assert.Equal(t, MinIdentity, id, "fresh table must allocate from MinIdentity")
		})
	}
}

// Corrupt persisted tables fail construction — enforcing routes against an
// ambiguous table would silently misroute, so this is a startup-gate error.
func TestIdentityAllocator_CorruptTableRejected(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "identity below reserved band",
			yaml:    "entries:\n    - dst: github.com\n      id: 5\nnext: 300\n",
			wantErr: "out-of-range identity 5",
		},
		{
			name:    "two dsts sharing one identity",
			yaml:    "entries:\n    - dst: github.com\n      id: 300\n    - dst: gitlab.com\n      id: 300\nnext: 301\n",
			wantErr: "identity 300 held by both",
		},
		{
			name:    "duplicate dst",
			yaml:    "entries:\n    - dst: github.com\n      id: 300\n    - dst: github.com\n      id: 301\nnext: 302\n",
			wantErr: `"github.com" listed twice`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := configmocks.NewIsolatedTestConfig(t)
			writeIdentityFile(t, cfg, tt.yaml)
			store, err := NewIdentityStore(cfg)
			require.NoError(t, err)
			_, err = NewIdentityAllocator(store)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// An unreadable table (schema-invalid field) surfaces as a construction
// error, whichever layer catches it first — never a silently empty table.
func TestIdentityAllocator_StoreReadFailure(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	writeIdentityFile(t, cfg, "entries:\n    - dst: github.com\n      id: 300\nnext: not-a-number\n")
	store, err := NewIdentityStore(cfg)
	if err != nil {
		return // store construction itself rejects the file — gate held
	}
	_, err = NewIdentityAllocator(store)
	require.Error(t, err)
}

// A zero-value allocator (constructor bypassed) must error, not panic on
// nil-map assignment — CP code degrades, never crashes.
func TestIdentityAllocator_ZeroValueErrors(t *testing.T) {
	var a IdentityAllocator
	require.ErrorContains(t, a.SyncDsts([]string{"github.com"}), "not constructed")
}

// Acceptance bar from the initiative: thousands of dsts, all unique.
func TestIdentityAllocator_UniquenessAtScale(t *testing.T) {
	a := newTestAllocator(t)
	dsts := make([]string, 0, 5000)
	for i := range 5000 {
		dsts = append(dsts, fmt.Sprintf("host-%04d.example.com", i))
	}
	require.NoError(t, a.SyncDsts(dsts))

	seen := map[ebpf.RouteIdentity]string{}
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
	require.NoError(t, a.SyncDsts([]string{".example.com"}))

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
	require.NoError(t, a.SyncDsts([]string{"203.0.113.7", "github.com"}))
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
	require.NoError(t, a.SyncDsts([]string{"github.com"}))
	id, ok := a.IdentityFor("github.com")
	require.True(t, ok)

	dst, ok := a.DomainFor(id)
	require.True(t, ok)
	assert.Equal(t, "github.com", dst)

	_, ok = a.DomainFor(id + 1)
	assert.False(t, ok)

	require.NoError(t, a.SyncDsts(nil))
	_, ok = a.DomainFor(id)
	assert.False(t, ok, "released identity must not reverse-resolve")
}
