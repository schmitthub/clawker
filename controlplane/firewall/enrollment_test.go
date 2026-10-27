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
