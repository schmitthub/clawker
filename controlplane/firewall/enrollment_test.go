package firewall_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// TestEnrollmentStore_RoundTrip covers the file-backed store the CP keeps
// beside the route identity table: a record the CLI pushed on FirewallEnable
// is durable across a reopen (the CP restart replay reads it), a later
// enable overwrites it, and an evict removes it. A container with no record
// reports found=false, which the handler treats as "no bypass".
func TestEnrollmentStore_RoundTrip(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := firewall.NewEnrollmentStore(cfg)
	require.NoError(t, err)

	const cid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	_, found, err := store.HostProxy(cid)
	require.NoError(t, err)
	assert.False(t, found, "fresh store holds no record")

	require.NoError(t, store.SetHostProxy(cid, true))
	enabled, found, err := store.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, enabled)

	// Reopen from disk: the record is what a restarted CP replays.
	reopened, err := firewall.NewEnrollmentStore(cfg)
	require.NoError(t, err)
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found, "record must survive a reopen")
	assert.True(t, enabled)

	// A later enable with the lane disabled overwrites the record.
	require.NoError(t, reopened.SetHostProxy(cid, false))
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, enabled)

	// Other containers are independent records.
	const other = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	require.NoError(t, reopened.SetHostProxy(other, true))
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, enabled, "a write for another container must not touch this record")

	require.NoError(t, reopened.Remove(cid))
	_, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.False(t, found, "evicted record must be gone")
	_, found, err = reopened.HostProxy(other)
	require.NoError(t, err)
	assert.True(t, found, "removing one record must leave the others")

	// Removing an absent record is a no-op: a destroy event can arrive for
	// a container that never enrolled.
	require.NoError(t, reopened.Remove(cid))

	// The removal reached disk.
	dataDir, err := cfg.FirewallDataSubdir()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(dataDir, consts.FirewallEnrollmentsFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), cid)
	assert.Contains(t, string(raw), other)
}

// TestEnrollmentStore_FailedWriteRestores: a write that does not reach disk
// must leave no trace in memory. The replay sweep and an unset
// FirewallEnable read the in-memory table, and the next successful write
// for any container persists every staged change, so an uncommitted grant
// would install a bypass that the failed request never got. The in-memory
// store seam has no write path, which is exactly the failure shape.
func TestEnrollmentStore_FailedWriteRestores(t *testing.T) {
	const seed = "containers:\n  ctr-on:\n    host_proxy: true\n  ctr-off:\n    host_proxy: false\n"
	grant := func(s firewall.EnrollmentStore, cid string) error { return s.SetHostProxy(cid, true) }
	revoke := func(s firewall.EnrollmentStore, cid string) error { return s.SetHostProxy(cid, false) }
	remove := func(s firewall.EnrollmentStore, cid string) error { return s.Remove(cid) }
	cases := []struct {
		name      string
		cid       string
		op        func(firewall.EnrollmentStore, string) error
		wantFound bool
		wantOn    bool
	}{
		{"new grant is dropped", "ctr-new", grant, false, false},
		{"grant over a false record keeps false", "ctr-off", grant, true, false},
		{"revoke over a true record keeps true", "ctr-on", revoke, true, true},
		{"remove keeps the record", "ctr-on", remove, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := firewall.NewEnrollmentStoreFromString(seed)
			require.NoError(t, err)

			require.Error(t, tc.op(store, tc.cid))

			enabled, found, err := store.HostProxy(tc.cid)
			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, found, "found")
			assert.Equal(t, tc.wantOn, enabled, "host proxy")
		})
	}
}
