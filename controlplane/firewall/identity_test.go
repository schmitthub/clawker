package firewall_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
	fwmocks "github.com/schmitthub/clawker/controlplane/firewall/mocks"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
)

// A failed persist must not be masked by the in-memory maps already holding the
// new table: the next sync — even a no-change one — retries the write, so the
// table reaches disk and a restart cannot renumber live identities.
//
// The failure is injected through RouteIdentityStoreMock rather than staged on
// the filesystem: a permissions-based failure is invisible to root, which is
// exactly the environment the CP and its containers run as. The mock fails the
// first SetTable and then delegates to a real file-backed store, so the retry's
// arrival on disk is still proven end-to-end.
func TestIdentityAllocator_PersistFailureRetriedOnNextSync(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	backing, err := firewall.NewIdentityStore(cfg)
	require.NoError(t, err)

	failNextWrite := true
	store := &fwmocks.RouteIdentityStoreMock{
		EntriesFunc: backing.Entries,
		CursorFunc:  backing.Cursor,
		SetTableFunc: func(entries []firewall.IdentityEntry, cursor int64) error {
			if failNextWrite {
				failNextWrite = false
				return errors.New("transient write failure")
			}
			return backing.SetTable(entries, cursor)
		},
	}

	a, err := firewall.NewIdentityAllocator(store)
	require.NoError(t, err)

	dsts := []string{"github.com", "gitlab.com"}
	require.Error(t, a.SyncDsts(dsts), "a failed persist must surface to the caller")

	// Same dst set: no in-memory change, but the owed persist must retry.
	require.NoError(t, a.SyncDsts(dsts))
	assert.Len(t, store.SetTableCalls(), 2, "the owed persist retried on the next sync")

	reopened, err := firewall.NewIdentityStore(cfg)
	require.NoError(t, err)
	b, err := firewall.NewIdentityAllocator(reopened)
	require.NoError(t, err)
	assert.Equal(t, a.Snapshot(), b.Snapshot(), "retried persist did not reach disk")
	assert.Len(t, b.Snapshot(), 2)
}

// SetTable rejects exactly what NewIdentityAllocator refuses to adopt. Without
// this the write front door would accept a table that bricks the NEXT CP boot,
// where the operator has no context for the failure and every enrolled agent is
// already fail-closed.
func TestRouteIdentityStore_SetTableRejectsCorruptTable(t *testing.T) {
	cases := []struct {
		name    string
		entries []firewall.IdentityEntry
		wantErr string
	}{
		{
			name:    "identity below the reserved band",
			entries: []firewall.IdentityEntry{{Dst: "github.com", ID: 5}},
			wantErr: "out-of-range identity 5",
		},
		{
			name: "two dsts sharing one identity",
			entries: []firewall.IdentityEntry{
				{Dst: "github.com", ID: 300},
				{Dst: "gitlab.com", ID: 300},
			},
			wantErr: "identity 300 held by both",
		},
		{
			name: "one dst listed twice",
			entries: []firewall.IdentityEntry{
				{Dst: "github.com", ID: 300},
				{Dst: "github.com", ID: 301},
			},
			wantErr: `"github.com" listed twice`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := configmocks.NewIsolatedTestConfig(t)
			dataDir, err := cfg.FirewallDataSubdir()
			require.NoError(t, err)
			store, err := firewall.NewIdentityStore(cfg)
			require.NoError(t, err)

			require.ErrorContains(t, store.SetTable(tc.entries, 302), tc.wantErr)
			assert.NoFileExists(t,
				filepath.Join(dataDir, consts.RouteIdentitiesFile),
				"a rejected table must not reach disk")
		})
	}
}
