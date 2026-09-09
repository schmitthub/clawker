package db_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

func openTestDatabase(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "grants.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})
	return database
}

func openTestStore(t *testing.T) *db.SocketGrantSQLStore {
	t.Helper()
	return db.NewSocketGrantStore(openTestDatabase(t), nil)
}

func testDecl(purpose string) config.HarnessSocket {
	return config.HarnessSocket{
		Source:    "",
		Target:    "",
		Optional:  false,
		Purpose:   purpose,
		Container: config.HarnessSocketContainer{Group: "", Mode: ""},
	}
}

func testIdentity(uid, gid int) socketbridge.ListenerIdentity {
	return socketbridge.ListenerIdentity{
		UID:   uid,
		GID:   gid,
		Owner: fmt.Sprintf("owner-%d", uid),
		Group: fmt.Sprintf("group-%d", gid),
	}
}

func TestSocketGrantStoreHonorsCanceledContext(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.ListSocketGrants(ctx)

	require.ErrorIs(t, err, context.Canceled)
}

func TestSocketGrantStoreUsesInjectedLogger(t *testing.T) {
	var output bytes.Buffer
	store := db.NewSocketGrantStore(openTestDatabase(t), logger.NewWriter(&output))

	err := store.GrantSocket(
		context.Background(),
		"/harness/acme",
		"acme",
		"/run/acme.sock",
		testDecl("Acme socket."),
		testIdentity(1, 2),
	)

	require.NoError(t, err)
	assert.Contains(t, output.String(), `"event":"socket_grant_write"`)
}

func TestRevokeSocketReturnsCountAndLogsGrantID(t *testing.T) {
	var output bytes.Buffer
	store := db.NewSocketGrantStore(openTestDatabase(t), logger.NewWriter(&output))
	require.NoError(t, store.GrantSocket(
		t.Context(),
		"/harness/acme",
		"acme",
		"/run/acme.sock",
		testDecl("Acme socket."),
		testIdentity(1, 2),
	))
	grant, err := store.LookupSocketGrant(t.Context(), "/harness/acme", "/run/acme.sock")
	require.NoError(t, err)
	output.Reset()

	count, err := store.RevokeSocket(t.Context(), grant.ID)

	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.Contains(t, output.String(), `"grant_id":`+strconv.FormatInt(grant.ID, 10))
	assert.Contains(t, output.String(), `"rows":1`)

	count, err = store.RevokeSocket(t.Context(), grant.ID)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestRevokeHarnessSocketsLogsHarnessPath(t *testing.T) {
	var output bytes.Buffer
	store := db.NewSocketGrantStore(openTestDatabase(t), logger.NewWriter(&output))
	require.NoError(t, store.GrantSocket(
		t.Context(),
		"/harness/acme",
		"acme",
		"/run/acme.sock",
		testDecl("Acme socket."),
		testIdentity(1, 2),
	))
	output.Reset()

	count, err := store.RevokeHarnessSockets(t.Context(), "/harness/acme")

	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.Contains(t, output.String(), `"harness_path":"/harness/acme"`)
}

func TestSocketGrantRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		status string
		write  func(context.Context, db.SocketGrantStore, string, string, string, config.HarnessSocket, socketbridge.ListenerIdentity) error
	}{
		{
			name:   "allow",
			status: db.GrantAllow,
			write: func(ctx context.Context, store db.SocketGrantStore, harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
				return store.GrantSocket(ctx, harnessPath, harnessName, hostPath, declaration, identity)
			},
		},
		{
			name:   "deny",
			status: db.GrantDeny,
			write: func(ctx context.Context, store db.SocketGrantStore, harnessPath, harnessName, hostPath string, declaration config.HarnessSocket, identity socketbridge.ListenerIdentity) error {
				return store.DenySocket(ctx, harnessPath, harnessName, hostPath, declaration, identity)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			decl := testDecl("Connects to the test daemon.")
			identity := testIdentity(1001, 1002)

			require.NoError(t, tc.write(t.Context(), store, "/harness/acme", "acme", "/run/acme.sock", decl, identity))

			grant, err := store.LookupSocketGrant(t.Context(), "/harness/acme", "/run/acme.sock")
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
	store := openTestStore(t)

	grant, err := store.LookupSocketGrant(t.Context(), "/harness/missing", "/run/missing.sock")

	require.ErrorIs(t, err, db.ErrGrantNotFound)
	assert.Nil(t, grant)
}

func TestSocketGrantUpsert(t *testing.T) {
	store := openTestStore(t)
	firstIdentity := testIdentity(1001, 1002)
	secondIdentity := testIdentity(2001, 2002)

	require.NoError(
		t,
		store.GrantSocket(
			t.Context(),
			"/harness/acme",
			"old-name",
			"/run/acme.sock",
			testDecl("Old purpose."),
			firstIdentity,
		),
	)
	before, err := store.LookupSocketGrant(t.Context(), "/harness/acme", "/run/acme.sock")
	require.NoError(t, err)
	require.NotNil(t, before)

	require.NoError(
		t,
		store.DenySocket(
			t.Context(),
			"/harness/acme",
			"new-name",
			"/run/acme.sock",
			testDecl("New purpose."),
			secondIdentity,
		),
	)
	after, err := store.LookupSocketGrant(t.Context(), "/harness/acme", "/run/acme.sock")
	require.NoError(t, err)
	require.NotNil(t, after)

	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, db.GrantDeny, after.Status)
	assert.Equal(t, "new-name", after.HarnessName)
	assert.Equal(t, secondIdentity, after.Identity)
	assert.Equal(t, "New purpose.", after.Purpose)
}

func TestRevokeSocketDoesNotReuseID(t *testing.T) {
	store := openTestStore(t)
	require.NoError(
		t,
		store.GrantSocket(t.Context(), "/harness/one", "one", "/run/one.sock", testDecl("One."), testIdentity(1, 1)),
	)
	first, err := store.LookupSocketGrant(t.Context(), "/harness/one", "/run/one.sock")
	require.NoError(t, err)
	require.NotNil(t, first)

	rows, revokeErr := store.RevokeSocket(t.Context(), first.ID)
	require.NoError(t, revokeErr)
	assert.Equal(t, int64(1), rows)
	missing, err := store.LookupSocketGrant(t.Context(), "/harness/one", "/run/one.sock")
	require.ErrorIs(t, err, db.ErrGrantNotFound)
	assert.Nil(t, missing)

	require.NoError(
		t,
		store.GrantSocket(t.Context(), "/harness/two", "two", "/run/two.sock", testDecl("Two."), testIdentity(2, 2)),
	)
	second, err := store.LookupSocketGrant(t.Context(), "/harness/two", "/run/two.sock")
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Greater(t, second.ID, first.ID)
}

func TestRevokeHarnessAndAllSockets(t *testing.T) {
	var output bytes.Buffer
	store := db.NewSocketGrantStore(openTestDatabase(t), logger.NewWriter(&output))
	for _, row := range []struct {
		harnessPath string
		hostPath    string
	}{
		{harnessPath: "/harness/one", hostPath: "/run/one-a.sock"},
		{harnessPath: "/harness/one", hostPath: "/run/one-b.sock"},
		{harnessPath: "/harness/two", hostPath: "/run/two.sock"},
	} {
		require.NoError(
			t,
			store.GrantSocket(
				t.Context(),
				row.harnessPath,
				"test",
				row.hostPath,
				testDecl(row.hostPath),
				testIdentity(1, 1),
			),
		)
	}

	rows, revokeErr := store.RevokeHarnessSockets(t.Context(), "/harness/one")
	require.NoError(t, revokeErr)
	assert.Equal(t, int64(2), rows)
	grants, err := store.ListSocketGrants(t.Context())
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "/harness/two", grants[0].HarnessPath)

	output.Reset()
	rows, revokeErr = store.RevokeAllSockets(t.Context())
	require.NoError(t, revokeErr)
	assert.Equal(t, int64(1), rows)
	assert.Contains(t, output.String(), `"scope":"all"`)
	assert.Contains(t, output.String(), `"rows":1`)
	grants, err = store.ListSocketGrants(t.Context())
	require.NoError(t, err)
	assert.Empty(t, grants)
}

func TestPruneHarnessSockets(t *testing.T) {
	store := openTestStore(t)
	for _, row := range []struct {
		harnessPath string
		hostPath    string
	}{
		{harnessPath: "/harness/one", hostPath: "/run/keep.sock"},
		{harnessPath: "/harness/one", hostPath: "/run/remove.sock"},
		{harnessPath: "/harness/two", hostPath: "/run/other.sock"},
	} {
		require.NoError(
			t,
			store.GrantSocket(
				t.Context(),
				row.harnessPath,
				"test",
				row.hostPath,
				testDecl(row.hostPath),
				testIdentity(1, 1),
			),
		)
	}

	require.NoError(t, store.PruneHarnessSockets(t.Context(), "/harness/one", []string{"/run/keep.sock"}))
	grants, err := store.ListSocketGrants(t.Context())
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
	firstStore := db.NewSocketGrantStore(first, nil)
	secondStore := db.NewSocketGrantStore(second, nil)
	ctx := t.Context()
	writeRows := func(store db.SocketGrantStore, prefix string) {
		defer wg.Done()
		<-start
		for i := range writesPerStore {
			hostPath := fmt.Sprintf("/run/%s-%d.sock", prefix, i)
			errs <- store.GrantSocket(ctx, "/harness/"+prefix, prefix, hostPath, testDecl(hostPath), testIdentity(i, i))
		}
	}

	wg.Add(2)
	go writeRows(firstStore, "first")
	go writeRows(secondStore, "second")
	close(start)
	wg.Wait()
	close(errs)
	for writeErr := range errs {
		require.NoError(t, writeErr)
	}

	grants, err := firstStore.ListSocketGrants(t.Context())
	require.NoError(t, err)
	assert.Len(t, grants, writesPerStore*2)
}
