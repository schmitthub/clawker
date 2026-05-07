package agent

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/logger"
)

// schemaSQL pins the agents table layout. Both thumbprint_hex and
// container_id are individually UNIQUE: a given cert can bind to
// exactly one container, a given container can host exactly one
// registration. The composite PK makes the binding intent explicit
// and prevents orphan rows where one half is set without the other.
//
// canonical_cn is the pre-computed "clawker[.<project>].<agent>"
// composed by auth.CanonicalAgentCN at Add time from typed inputs.
// Storing it as a column eliminates the historical Lookup-time panic
// vector where MustProjectSlug/MustAgentName reconstructed the CN
// against on-disk strings — a single malformed row used to crash CP.
//
// The (project, agent_name) index serves diagnostic and CLI listing
// queries; uniqueness is NOT enforced at the schema layer because
// Docker container name uniqueness handles the real conflict upstream
// and the dockerevents eviction race may briefly leave two rows with
// the same (project, agent_name) before the old one is deleted.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  canonical_cn   TEXT NOT NULL,
  registered_at  INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX IF NOT EXISTS idx_name_project ON agents(project, agent_name);
`

// sqliteRegistry is a Registry whose authoritative state lives in a
// sqlite database. There is no in-memory cache: every read hits sqlite
// directly. modernc/sqlite + WAL on local fs makes per-RPC queries
// sub-millisecond, and removing the cache eliminates the entire class
// of mirror/disk drift bugs (orphan rows, partial-evict invariant
// violations, malformed-row resurrection on reload).
type sqliteRegistry struct {
	db  *sql.DB
	log *logger.Logger
}

// NewSQLiteWriter opens (or creates) the sqlite database at dbPath,
// applies the schema, and returns a registry suitable for the writer
// process. The clawker CLI is the row creator and the CP is the row
// evictor; both open the DB via this constructor and rely on sqlite's
// file lock + busy_timeout to absorb transient contention.
//
// The parent directory of dbPath must already exist.
func NewSQLiteWriter(dbPath string, log *logger.Logger) (Registry, error) {
	return openSQLite(dbPath, log, sqliteOpenWriter)
}

// NewSQLiteReader opens an existing sqlite database at dbPath in
// read-only mode and returns a registry whose write methods (Add,
// EvictByContainerID) all fail with the underlying sqlite "attempt to
// write a readonly database" error. Used by the local-read CLI
// (`clawker controlplane agents`) to consume the registry without
// risking accidental mutation.
func NewSQLiteReader(dbPath string, log *logger.Logger) (Registry, error) {
	return openSQLite(dbPath, log, sqliteOpenReader)
}

// NewSQLite is retained for tests + paths that haven't migrated to
// the writer/reader split. New call sites should use NewSQLiteWriter
// or NewSQLiteReader explicitly so the open mode is obvious at the
// wiring point.
//
// Deprecated: use NewSQLiteWriter or NewSQLiteReader.
func NewSQLite(dbPath string, log *logger.Logger) (Registry, error) {
	return NewSQLiteWriter(dbPath, log)
}

// EnsureSchema opens the database in writer mode (creating the file
// if missing), applies the schema, and closes. Called by host-side
// CP bootstrap so that CP — which may open read-only — finds an
// existing schema-applied DB even when no CLI write has happened yet
// (e.g. the user runs `clawker controlplane up` before any `clawker
// run`).
func EnsureSchema(dbPath string, log *logger.Logger) error {
	r, err := NewSQLiteWriter(dbPath, log)
	if err != nil {
		return err
	}
	if closer, ok := r.(*sqliteRegistry); ok {
		return closer.Close()
	}
	return nil
}

type sqliteOpenMode int

const (
	sqliteOpenWriter sqliteOpenMode = iota
	sqliteOpenReader
)

func openSQLite(dbPath string, log *logger.Logger, mode sqliteOpenMode) (Registry, error) {
	if log == nil {
		log = logger.Nop()
	}
	if dbPath == "" {
		return nil, errors.New("agentregistry: open called with empty dbPath")
	}
	parent := filepath.Dir(dbPath)
	if _, err := os.Stat(parent); err != nil {
		return nil, fmt.Errorf("agentregistry: parent dir %s missing: %w", parent, err)
	}

	// `_pragma=` query parameters get applied as PRAGMA statements at
	// connection time. WAL gives us non-blocking readers alongside the
	// single writer, busy_timeout prevents transient contention from
	// raising SQLITE_BUSY at the application layer, and foreign_keys
	// is on so future child tables can rely on cascading deletes.
	var dsn string
	switch mode {
	case sqliteOpenReader:
		// `mode=ro` opens the file read-only; `query_only(true)` is a
		// belt-and-suspenders runtime guard that blocks write
		// statements on the connection even if a future code path
		// mistakenly attempts one. The DB must already exist — reader
		// open does not auto-create.
		if _, err := os.Stat(dbPath); err != nil {
			return nil, fmt.Errorf("agentregistry: db %s missing: %w", dbPath, err)
		}
		dsn = dbPath + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(true)"
	default:
		dsn = dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: open sqlite: %w", err)
	}
	if mode == sqliteOpenWriter {
		// Single writer connection. modernc/sqlite is goroutine-safe,
		// but a single connection serialises writes which matches the
		// single-writer model the design assumes.
		db.SetMaxOpenConns(1)
		if err := applySchema(db, log); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("agentregistry: apply schema: %w", err)
		}
	}

	r := &sqliteRegistry{
		db:  db,
		log: log,
	}
	rowCount, countErr := r.countRows()
	if countErr != nil {
		// SELECT COUNT failure right after a fresh schema apply is
		// suspicious (corrupt page, lock contention, busted PRAGMA).
		// Surface at Warn so the boot-log "rows=N" line is not
		// misread as an authoritative empty-DB signal when the
		// reader actually couldn't ask.
		r.log.Warn().Err(countErr).
			Str("db_path", dbPath).
			Msg("agentregistry: countRows failed at open; row count is unknown")
	}
	r.log.Info().
		Str("db_path", dbPath).
		Str("mode", openModeName(mode)).
		Int("rows", rowCount).
		Msg("agentregistry: sqlite registry opened")
	return r, nil
}

func openModeName(mode sqliteOpenMode) string {
	if mode == sqliteOpenReader {
		return "reader"
	}
	return "writer"
}

// applySchema applies the schema atomically. If an existing DB lacks
// the canonical_cn column (pre-Task-01 schema), the table is dropped
// and recreated — alpha-only migration policy: the registry is
// regenerable from the next clawker run/start, and a partial migration
// is worse than a forced re-create of every affected container.
func applySchema(db *sql.DB, log *logger.Logger) error {
	if needsMigration, err := schemaMissingCanonicalCN(db); err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	} else if needsMigration {
		log.Warn().Msg("agentregistry: schema mismatch (missing canonical_cn); dropping and recreating agents table")
		if _, err := db.Exec(`DROP TABLE IF EXISTS agents`); err != nil {
			return fmt.Errorf("drop legacy agents table: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema tx: %w", err)
	}
	if _, err := tx.Exec(schemaSQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema tx: %w", err)
	}
	return nil
}

// schemaMissingCanonicalCN returns true when an `agents` table exists
// but does NOT carry the canonical_cn column. Returns false when the
// table is absent (fresh DB — schemaSQL CREATE-IF-NOT-EXISTS handles
// it) or when the column is present.
func schemaMissingCanonicalCN(db *sql.DB) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(agents)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	hasCanonical := false
	hasAnyColumn := false
	for rows.Next() {
		var (
			cid            int
			name, typeName string
			notNull, pk    int
			dfltValue      sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &dfltValue, &pk); err != nil {
			return false, err
		}
		hasAnyColumn = true
		if name == "canonical_cn" {
			hasCanonical = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return hasAnyColumn && !hasCanonical, nil
}

func (r *sqliteRegistry) countRows() (int, error) {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *sqliteRegistry) Add(entry Entry) error {
	validateEntry(entry)
	cn, err := canonicalCNFromEntry(entry)
	if err != nil {
		return err
	}

	tpHex := hex.EncodeToString(entry.Thumbprint[:])
	if entry.LastSeen.IsZero() {
		entry.LastSeen = entry.RegisteredAt
	}

	// The composite PK + UNIQUE constraints do the real work — a stale
	// row for the same thumbprint OR container_id surfaces as a
	// constraint violation and the handler chooses how to react
	// (typically: alert + evict by container_id + retry).
	_, err = r.db.Exec(`
		INSERT INTO agents (thumbprint_hex, container_id, agent_name, project, canonical_cn, registered_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		tpHex,
		entry.ContainerID,
		entry.AgentName,
		entry.Project,
		cn,
		entry.RegisteredAt.Unix(),
		entry.LastSeen.Unix(),
	)
	if err != nil {
		return fmt.Errorf("agentregistry: persist entry: %w", err)
	}

	r.log.Info().
		Str("agent", entry.AgentName).
		Str("project", entry.Project).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
	return nil
}

// scanEntry reads a row's columns into an Entry. Caller supplies the
// scanner so this works for both QueryRow (single-row reads) and Rows
// (Snapshot iteration).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(s rowScanner) (Entry, error) {
	var (
		tpHex, containerID, agentName, project string
		registeredAt, lastSeen                 int64
	)
	if err := s.Scan(&tpHex, &containerID, &agentName, &project, &registeredAt, &lastSeen); err != nil {
		return Entry{}, err
	}
	var tp [sha256.Size]byte
	raw, err := hex.DecodeString(tpHex)
	if err != nil || len(raw) != sha256.Size {
		return Entry{}, fmt.Errorf("agentregistry: malformed thumbprint_hex %q", tpHex)
	}
	copy(tp[:], raw)
	return Entry{
		AgentName:    agentName,
		Project:      project,
		ContainerID:  containerID,
		Thumbprint:   tp,
		RegisteredAt: time.Unix(registeredAt, 0).UTC(),
		LastSeen:     time.Unix(lastSeen, 0).UTC(),
	}, nil
}

const selectEntryCols = `thumbprint_hex, container_id, agent_name, project, registered_at, last_seen`

func (r *sqliteRegistry) LookupByContainerID(containerID string) (*Entry, error) {
	if containerID == "" {
		return nil, ErrUnknownAgent
	}
	row := r.db.QueryRow(`SELECT `+selectEntryCols+` FROM agents WHERE container_id = ?`, containerID)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownAgent
	}
	if err != nil {
		return nil, fmt.Errorf("agentregistry: query LookupByContainerID: %w", err)
	}
	return &e, nil
}

func (r *sqliteRegistry) Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error) {
	tpHex := hex.EncodeToString(thumbprint[:])
	var (
		containerID, agentName, project, canonical string
		registeredAt, lastSeen                     int64
	)
	err := r.db.QueryRow(`
		SELECT container_id, agent_name, project, canonical_cn, registered_at, last_seen
		FROM agents WHERE thumbprint_hex = ?`, tpHex,
	).Scan(&containerID, &agentName, &project, &canonical, &registeredAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownAgent
	}
	if err != nil {
		return nil, fmt.Errorf("agentregistry: query Lookup: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(cn), []byte(canonical)) != 1 {
		return nil, ErrUnknownAgent
	}
	return &Entry{
		AgentName:    agentName,
		Project:      project,
		ContainerID:  containerID,
		Thumbprint:   thumbprint,
		RegisteredAt: time.Unix(registeredAt, 0).UTC(),
		LastSeen:     time.Unix(lastSeen, 0).UTC(),
	}, nil
}

func (r *sqliteRegistry) EvictByContainerID(containerID string) error {
	if containerID == "" {
		return nil
	}
	res, err := r.db.Exec(`DELETE FROM agents WHERE container_id = ?`, containerID)
	if err != nil {
		return fmt.Errorf("agentregistry: delete by container_id: %w", err)
	}
	rows, raErr := res.RowsAffected()
	if raErr != nil {
		// Surface RowsAffected failure to the caller so reaper-driven
		// errors.Join sweeps see it (Reap aggregates EvictByContainerID
		// errors). The DELETE itself succeeded but we cannot confirm
		// scope — operators investigating "why didn't this row evict?"
		// need the typed-error path, not just a Warn line.
		return fmt.Errorf("agentregistry: RowsAffected after evict (container_id=%s): %w", containerID, raErr)
	}
	if rows > 0 {
		r.log.Info().
			Int64("rows", rows).
			Str("container_id", containerID).
			Msg("agentregistry: agent evicted")
	}
	return nil
}

func (r *sqliteRegistry) Snapshot() []Entry {
	rows, err := r.db.Query(`SELECT ` + selectEntryCols + ` FROM agents`)
	if err != nil {
		r.log.Error().Err(err).Msg("agentregistry: query snapshot failed")
		return nil
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			r.log.Error().
				Err(err).
				Msg("agentregistry: skipping malformed row in Snapshot")
			continue
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		r.log.Error().Err(err).Msg("agentregistry: snapshot rows iteration failed")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].AgentName < out[j].AgentName
	})
	return out
}

// Close releases the underlying sql.DB handle. Used by tests; the CP
// daemon holds the registry for its full process lifetime so there is
// no production caller for Close today.
func (r *sqliteRegistry) Close() error { return r.db.Close() }
