package agent

import (
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

// dbPath returns a fresh sqlite DB path under the test's temp dir.
func dbPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// closeRegistry releases the underlying *sql.DB handle if the concrete
// type exposes Close. Safe no-op for in-memory registries.
func closeRegistry(t *testing.T, r Registry) {
	t.Helper()
	if c, ok := r.(*sqliteRegistry); ok {
		require.NoError(t, c.Close())
	}
}

func TestSQLiteWriter_Add_RejectsDuplicates(t *testing.T) {
	cases := []struct {
		name   string
		first  Entry
		second Entry
		reason string
	}{
		{
			name:   "duplicate thumbprint",
			first:  validEntry("p", "a", "ctr-1", "cert-shared"),
			second: validEntry("p", "a", "ctr-2", "cert-shared"),
			reason: "UNIQUE on thumbprint_hex must reject",
		},
		{
			name:   "duplicate container_id",
			first:  validEntry("p", "a", "ctr-shared", "cert-1"),
			second: validEntry("p", "b", "ctr-shared", "cert-2"),
			reason: "UNIQUE on container_id must reject",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
			require.NoError(t, err)
			t.Cleanup(func() { closeRegistry(t, r) })

			require.NoError(t, r.Add(tc.first))
			require.Error(t, r.Add(tc.second), tc.reason)
		})
	}
}

func TestSQLiteRegistry_EvictByContainerID_DeletesRow(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	require.NoError(t, r.Add(validEntry("p", "a", "ctr-1", "cert-1")))
	require.NoError(t, r.Add(validEntry("p", "b", "ctr-2", "cert-2")))

	// Empty-string and missing-row paths collapse to ErrUnknownAgent —
	// the registry's idempotency contract for the Register handler.
	_, err = r.LookupByContainerID("ctr-missing")
	assert.ErrorIs(t, err, ErrUnknownAgent)
	_, err = r.LookupByContainerID("")
	assert.ErrorIs(t, err, ErrUnknownAgent)

	// Pre-evict lookup exercises the sqlite hex→[32]byte thumbprint
	// round-trip — a regression in scanEntry's hex.DecodeString /
	// length check would silently return zero-thumbprint entries.
	got, err := r.LookupByContainerID("ctr-1")
	require.NoError(t, err)
	assert.Equal(t, tp("cert-1"), got.Thumbprint, "thumbprint round-trips through sqlite")

	require.NoError(t, r.EvictByContainerID("ctr-1"))

	_, err = r.LookupByContainerID("ctr-1")
	assert.ErrorIs(t, err, ErrUnknownAgent)
	_, err = r.LookupByContainerID("ctr-2")
	require.NoError(t, err)
}

func TestSQLiteRegistry_EvictByContainerID_ReturnsErrOnDBFailure(t *testing.T) {
	// Closing the DB handle then calling Evict surfaces a real sqlite
	// error — the new return signature exists so reapers and the
	// dockerevents subscription can log it instead of silently
	// diverging from disk.
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	concrete, ok := r.(*sqliteRegistry)
	require.True(t, ok)

	require.NoError(t, r.Add(validEntry("p", "a", "ctr-1", "cert-1")))
	require.NoError(t, concrete.Close())

	err = r.EvictByContainerID("ctr-1")
	require.Error(t, err)
}

func TestSQLiteRegistry_Snapshot_Sorted(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	require.NoError(t, r.Add(validEntry("zproj", "dev", "ctr-1", "cert-1")))
	require.NoError(t, r.Add(validEntry("aproj", "dev", "ctr-2", "cert-2")))
	require.NoError(t, r.Add(validEntry("aproj", "bot", "ctr-3", "cert-3")))

	snap := r.Snapshot()
	require.Len(t, snap, 3)
	got := make([][2]string, len(snap))
	for i, e := range snap {
		got[i] = [2]string{e.Project, e.AgentName}
	}
	assert.Equal(t, [][2]string{
		{"aproj", "bot"},
		{"aproj", "dev"},
		{"zproj", "dev"},
	}, got)
}

func TestSQLiteRegistry_ConcurrentWriters(t *testing.T) {
	// Two NewSQLiteWriter opens against the same DB path. modernc/
	// sqlite uses the file-system lock + busy_timeout to serialize
	// writes so concurrent Add ops do not corrupt the table. UNIQUE
	// constraints will reject collisions, which we count and ignore.
	path := dbPath(t, "agents.db")
	w1, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, w1) })
	w2, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, w2) })

	const n = 24
	var wg sync.WaitGroup
	wg.Add(2 * n)
	var dupes atomic.Int32
	add := func(w Registry, base int) {
		defer wg.Done()
		suffix := string(rune('a' + base%26))
		entry := validEntry("p", "agent-"+suffix, "ctr-"+suffix, "cert-"+suffix)
		if err := w.Add(entry); err != nil {
			dupes.Add(1)
		}
	}
	for i := range n {
		go add(w1, i)
		go add(w2, i)
	}
	wg.Wait()

	// Both writers raced into the same set of rows; UNIQUE rejected
	// every duplicate.
	snap := w1.Snapshot()
	assert.GreaterOrEqual(t, len(snap), 1)
	assert.LessOrEqual(t, len(snap), n)
	assert.EqualValues(t, 2*n-len(snap), dupes.Load(), "every collision must surface as Add error")
}

func TestSQLiteRegistry_SchemaMigration_FromOldDB(t *testing.T) {
	// Open a sqlite DB with a pre-goose schema and seed a row. The
	// next NewSQLiteWriter open must detect the missing goose_db_version
	// table, drop the legacy agents table (alpha policy — registry
	// state regenerates on re-Register), and apply the embedded
	// migrations cleanly.
	path := dbPath(t, "agents.db")
	const oldSchema = `
		CREATE TABLE agents (
		  thumbprint_hex TEXT NOT NULL,
		  container_id   TEXT NOT NULL,
		  agent_name     TEXT NOT NULL,
		  project        TEXT NOT NULL,
		  registered_at  INTEGER NOT NULL,
		  last_seen      INTEGER NOT NULL,
		  PRIMARY KEY (thumbprint_hex, container_id)
		);
	`
	old, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	require.NoError(t, err)
	_, err = old.Exec(oldSchema)
	require.NoError(t, err)
	_, err = old.Exec(`INSERT INTO agents VALUES('legacy-hex','legacy-ctr','legacy-agent','legacy-proj',1,1)`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	// Open via the production path. Schema migration drops the legacy
	// row by design (alpha policy); re-Register on next clawker
	// run/start re-populates.
	r, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	assert.Empty(t, r.Snapshot(), "legacy rows must be dropped on schema migration")

	// New Adds against the migrated schema work.
	require.NoError(t, r.Add(validEntry("p", "a", "ctr-1", "cert-1")))
	got, err := r.LookupByContainerID("ctr-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "a", got.AgentName)
}
