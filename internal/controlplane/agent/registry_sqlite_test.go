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

func TestSQLiteWriter_Add_PersistsCanonicalCN(t *testing.T) {
	path := dbPath(t, "agents.db")

	w, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)

	entry := validEntry("alpha", "dev", "ctr-1", "cert-1")
	require.NoError(t, w.Add(entry))
	closeRegistry(t, w)

	// Re-open and verify the CN-gated Lookup round-trips against the
	// pre-computed canonical_cn column.
	r, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	got, err := r.Lookup(entry.Thumbprint, canonical("alpha", "dev"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "dev", got.AgentName)
	assert.Equal(t, "alpha", got.Project)
	assert.Equal(t, "ctr-1", got.ContainerID)
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

func TestSQLiteReader_RejectsWrites(t *testing.T) {
	path := dbPath(t, "agents.db")
	w, err := NewSQLiteWriter(path, logger.Nop())
	require.NoError(t, err)
	require.NoError(t, w.Add(validEntry("p", "a", "ctr-1", "cert-1")))
	closeRegistry(t, w)

	r, err := NewSQLiteReader(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	// Read works.
	got, err := r.Lookup(tp("cert-1"), canonical("p", "a"))
	require.NoError(t, err)
	require.NotNil(t, got)

	// Add fails — sqlite returns "attempt to write a readonly database".
	err = r.Add(validEntry("p", "b", "ctr-2", "cert-2"))
	require.Error(t, err)
}

func TestSQLiteRegistry_Lookup_CNMismatch_ReturnsUnknownAgent(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	require.NoError(t, r.Add(validEntry("alpha", "dev", "ctr", "cert")))

	// Right thumbprint, wrong CN — must collapse to ErrUnknownAgent so
	// handlers can't probe which half of the composite identity failed.
	_, err = r.Lookup(tp("cert"), canonical("beta", "dev"))
	assert.ErrorIs(t, err, ErrUnknownAgent)
	_, err = r.Lookup(tp("cert"), canonical("alpha", "other"))
	assert.ErrorIs(t, err, ErrUnknownAgent)

	// Right thumbprint + right CN — must succeed.
	got, err := r.Lookup(tp("cert"), canonical("alpha", "dev"))
	require.NoError(t, err)
	assert.Equal(t, "dev", got.AgentName)
}

func TestSQLiteRegistry_Lookup_UnknownThumbprint(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	_, err = r.Lookup(tp("never-registered"), "clawker.x.y")
	assert.ErrorIs(t, err, ErrUnknownAgent)
}

func TestSQLiteRegistry_LookupByContainerID(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	require.NoError(t, r.Add(validEntry("p", "a", "ctr-1", "cert-1")))

	got, err := r.LookupByContainerID("ctr-1")
	require.NoError(t, err)
	assert.Equal(t, "a", got.AgentName)

	_, err = r.LookupByContainerID("ctr-missing")
	assert.ErrorIs(t, err, ErrUnknownAgent)

	_, err = r.LookupByContainerID("")
	assert.ErrorIs(t, err, ErrUnknownAgent)
}

func TestSQLiteRegistry_EvictByContainerID_DeletesRow(t *testing.T) {
	r, err := NewSQLiteWriter(dbPath(t, "agents.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { closeRegistry(t, r) })

	require.NoError(t, r.Add(validEntry("p", "a", "ctr-1", "cert-1")))
	require.NoError(t, r.Add(validEntry("p", "b", "ctr-2", "cert-2")))

	require.NoError(t, r.EvictByContainerID("ctr-1"))

	_, err = r.Lookup(tp("cert-1"), canonical("p", "a"))
	assert.ErrorIs(t, err, ErrUnknownAgent)
	_, err = r.Lookup(tp("cert-2"), canonical("p", "b"))
	assert.NoError(t, err)
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
	// Open a sqlite DB with the OLD schema (no canonical_cn) and seed
	// a row. The next NewSQLiteWriter open must detect the drift,
	// drop+recreate the table, and continue.
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
	got, err := r.Lookup(tp("cert-1"), canonical("p", "a"))
	require.NoError(t, err)
	require.NotNil(t, got)
}
