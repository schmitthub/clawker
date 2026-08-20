package db_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "grants.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func testDecl(purpose string) config.HarnessSocket {
	return config.HarnessSocket{Purpose: purpose}
}

func testIdentity(uid, gid int) socketbridge.ListenerIdentity {
	return socketbridge.ListenerIdentity{
		UID:   uid,
		GID:   gid,
		Owner: fmt.Sprintf("owner-%d", uid),
		Group: fmt.Sprintf("group-%d", gid),
	}
}

func TestSocketGrantRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		status string
		write  func(*db.DB, string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error
	}{
		{name: "allow", status: db.GrantAllow, write: (*db.DB).GrantSocket},
		{name: "deny", status: db.GrantDeny, write: (*db.DB).DenySocket},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestDB(t)
			decl := testDecl("Connects to the test daemon.")
			identity := testIdentity(1001, 1002)

			require.NoError(t, tc.write(store, "/harness/acme", "acme", "/run/acme.sock", decl, identity))

			grant, err := store.LookupSocketGrant("/harness/acme", "/run/acme.sock")
			require.NoError(t, err)
			require.NotNil(t, grant)
			assert.Positive(t, grant.ID)
			assert.Equal(t, "/harness/acme", grant.HarnessPath)
			assert.Equal(t, "acme", grant.HarnessName)
			assert.Equal(t, "/run/acme.sock", grant.HostPath)
			assert.Equal(t, tc.status, grant.Status)
			assert.Equal(t, identity, grant.Identity)
			assert.Equal(t, decl.Purpose, grant.Purpose)
			assert.False(t, grant.GrantedAt.IsZero())
		})
	}
}

func TestLookupSocketGrantMissing(t *testing.T) {
	store := openTestDB(t)

	grant, err := store.LookupSocketGrant("/harness/missing", "/run/missing.sock")

	require.NoError(t, err)
	assert.Nil(t, grant)
}

func TestSocketGrantUpsert(t *testing.T) {
	store := openTestDB(t)
	firstIdentity := testIdentity(1001, 1002)
	secondIdentity := testIdentity(2001, 2002)

	require.NoError(t, store.GrantSocket("/harness/acme", "old-name", "/run/acme.sock", testDecl("Old purpose."), firstIdentity))
	before, err := store.LookupSocketGrant("/harness/acme", "/run/acme.sock")
	require.NoError(t, err)
	require.NotNil(t, before)

	require.NoError(t, store.DenySocket("/harness/acme", "new-name", "/run/acme.sock", testDecl("New purpose."), secondIdentity))
	after, err := store.LookupSocketGrant("/harness/acme", "/run/acme.sock")
	require.NoError(t, err)
	require.NotNil(t, after)

	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, db.GrantDeny, after.Status)
	assert.Equal(t, "new-name", after.HarnessName)
	assert.Equal(t, secondIdentity, after.Identity)
	assert.Equal(t, "New purpose.", after.Purpose)
}

func TestRevokeSocketDoesNotReuseID(t *testing.T) {
	store := openTestDB(t)
	require.NoError(t, store.GrantSocket("/harness/one", "one", "/run/one.sock", testDecl("One."), testIdentity(1, 1)))
	first, err := store.LookupSocketGrant("/harness/one", "/run/one.sock")
	require.NoError(t, err)
	require.NotNil(t, first)

	require.NoError(t, store.RevokeSocket(first.ID))
	missing, err := store.LookupSocketGrant("/harness/one", "/run/one.sock")
	require.NoError(t, err)
	assert.Nil(t, missing)

	require.NoError(t, store.GrantSocket("/harness/two", "two", "/run/two.sock", testDecl("Two."), testIdentity(2, 2)))
	second, err := store.LookupSocketGrant("/harness/two", "/run/two.sock")
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Greater(t, second.ID, first.ID)
}

func TestRevokeHarnessAndAllSockets(t *testing.T) {
	store := openTestDB(t)
	for _, row := range []struct {
		harnessPath string
		hostPath    string
	}{
		{harnessPath: "/harness/one", hostPath: "/run/one-a.sock"},
		{harnessPath: "/harness/one", hostPath: "/run/one-b.sock"},
		{harnessPath: "/harness/two", hostPath: "/run/two.sock"},
	} {
		require.NoError(t, store.GrantSocket(row.harnessPath, "test", row.hostPath, testDecl(row.hostPath), testIdentity(1, 1)))
	}

	require.NoError(t, store.RevokeHarnessSockets("/harness/one"))
	grants, err := store.ListSocketGrants()
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "/harness/two", grants[0].HarnessPath)

	require.NoError(t, store.RevokeAllSockets())
	grants, err = store.ListSocketGrants()
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestPruneHarnessSockets(t *testing.T) {
	store := openTestDB(t)
	for _, row := range []struct {
		harnessPath string
		hostPath    string
	}{
		{harnessPath: "/harness/one", hostPath: "/run/keep.sock"},
		{harnessPath: "/harness/one", hostPath: "/run/remove.sock"},
		{harnessPath: "/harness/two", hostPath: "/run/other.sock"},
	} {
		require.NoError(t, store.GrantSocket(row.harnessPath, "test", row.hostPath, testDecl(row.hostPath), testIdentity(1, 1)))
	}

	require.NoError(t, store.PruneHarnessSockets("/harness/one", []string{"/run/keep.sock"}))
	grants, err := store.ListSocketGrants()
	require.NoError(t, err)
	require.Len(t, grants, 2)
	assert.Equal(t, []string{"/run/keep.sock", "/run/other.sock"}, []string{grants[0].HostPath, grants[1].HostPath})
}

func TestConcurrentOpensDoNotLoseWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grants.db")
	first, err := db.Open(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := db.Open(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	const writesPerStore = 20
	start := make(chan struct{})
	errs := make(chan error, writesPerStore*2)
	var wg sync.WaitGroup
	writeRows := func(store *db.DB, prefix string) {
		defer wg.Done()
		<-start
		for i := range writesPerStore {
			hostPath := fmt.Sprintf("/run/%s-%d.sock", prefix, i)
			errs <- store.GrantSocket("/harness/"+prefix, prefix, hostPath, testDecl(hostPath), testIdentity(i, i))
		}
	}

	wg.Add(2)
	go writeRows(first, "first")
	go writeRows(second, "second")
	close(start)
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		require.NoError(t, writeErr)
	}

	grants, err := first.ListSocketGrants()
	require.NoError(t, err)
	assert.Len(t, grants, writesPerStore*2)
}
