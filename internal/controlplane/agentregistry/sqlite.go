package agentregistry

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
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/logger"
)

// schemaSQL pins the agents table layout (per the §5.1 design). Both
// thumbprint_hex and container_id are individually UNIQUE: a given
// cert can bind to exactly one container, a given container can host
// exactly one registration. The composite PK makes the binding intent
// explicit and prevents orphan rows where one half is set without the
// other. The (project, agent_name) index serves diagnostic and CLI
// listing queries; uniqueness is NOT enforced at the schema layer
// because Docker container name uniqueness handles the real conflict
// upstream and the dockerevents eviction race may briefly leave two
// rows with the same (project, agent_name) before the old one is
// deleted.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  registered_at  INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX IF NOT EXISTS idx_name_project ON agents(project, agent_name);
`

// sqliteRegistry is a Registry whose authoritative state lives in a
// sqlite database; an in-memory map mirrors the rows so Lookup and
// Snapshot stay on the hot path that AgentPort RPCs hit on every
// request via IdentityInterceptor. Add and EvictByContainerID write
// the database first; the in-memory mirror is updated only after the
// disk write commits, so a process crash can never produce a row in
// memory that does not survive a CP restart.
type sqliteRegistry struct {
	mu      sync.RWMutex
	entries map[[sha256.Size]byte]Entry
	db      *sql.DB
	log     *logger.Logger
}

// NewSQLiteWriter opens (or creates) the sqlite database at dbPath,
// applies the schema, and returns a registry suitable for the writer
// process. The clawker CLI is the sole authoritative writer of the
// agentregistry — Add and EvictByContainerID both succeed against the
// returned handle. Concurrent CLI invocations serialize via sqlite's
// file lock; busy_timeout absorbs transient contention.
//
// The parent directory of dbPath must already exist.
func NewSQLiteWriter(dbPath string, log *logger.Logger) (Registry, error) {
	return openSQLite(dbPath, log, sqliteOpenWriter)
}

// NewSQLiteReader opens an existing sqlite database at dbPath in
// read-only mode and returns a registry whose write methods (Add,
// EvictByContainerID) all fail with the underlying sqlite "attempt to
// write a readonly database" error. Used by the CP to consume the
// CLI-written registry without risking accidental mutation.
//
// The CP's bind mount is RW at the OS layer (so sqlite can read the
// `-wal` / `-shm` siblings), but `mode=ro&query_only=ON` enforces
// read-only at the application layer.
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
// CP bootstrap so that CP — which opens read-only — finds an existing
// schema-applied DB even when no CLI write has happened yet (e.g. the
// user runs `clawker controlplane up` before any `clawker run`).
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
		if _, err := db.Exec(schemaSQL); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("agentregistry: apply schema: %w", err)
		}
	}

	r := &sqliteRegistry{
		entries: make(map[[sha256.Size]byte]Entry),
		db:      db,
		log:     log,
	}
	if err := r.reload(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agentregistry: reload from sqlite: %w", err)
	}
	r.log.Info().
		Str("db_path", dbPath).
		Str("mode", openModeName(mode)).
		Int("rows", len(r.entries)).
		Msg("agentregistry: sqlite registry opened")
	return r, nil
}

func openModeName(mode sqliteOpenMode) string {
	if mode == sqliteOpenReader {
		return "reader"
	}
	return "writer"
}

// reload pulls every row off disk into the in-memory mirror. Called
// once at NewSQLite time. The mirror is the read-side authority for
// Lookup/Snapshot afterwards.
func (r *sqliteRegistry) reload() error {
	rows, err := r.db.Query(`
		SELECT thumbprint_hex, container_id, agent_name, project, registered_at, last_seen
		FROM agents
	`)
	if err != nil {
		return fmt.Errorf("query agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			tpHex, containerID, agentName, project string
			registeredAt, lastSeen                 int64
		)
		if err := rows.Scan(&tpHex, &containerID, &agentName, &project, &registeredAt, &lastSeen); err != nil {
			return fmt.Errorf("scan agent row: %w", err)
		}
		var tp [sha256.Size]byte
		raw, err := hex.DecodeString(tpHex)
		if err != nil || len(raw) != sha256.Size {
			// Bad row — log and skip rather than refuse to start. A
			// corrupted thumbprint is unrecoverable; the agent will
			// re-Register on its next boot and overwrite cleanly.
			r.log.Error().
				Str("thumbprint_hex", tpHex).
				Str("container_id", containerID).
				Msg("agentregistry: skipping row with malformed thumbprint_hex")
			continue
		}
		copy(tp[:], raw)
		r.entries[tp] = Entry{
			AgentName:    agentName,
			Project:      project,
			ContainerID:  containerID,
			Thumbprint:   tp,
			RegisteredAt: time.Unix(registeredAt, 0).UTC(),
			LastSeen:     time.Unix(lastSeen, 0).UTC(),
		}
	}
	return rows.Err()
}

func (r *sqliteRegistry) Add(entry Entry) error {
	validateEntry(entry)

	tpHex := hex.EncodeToString(entry.Thumbprint[:])
	if entry.LastSeen.IsZero() {
		entry.LastSeen = entry.RegisteredAt
	}

	// Disk before cache: if the DB rejects the write, the in-memory
	// mirror stays consistent with disk. The composite PK + UNIQUE
	// constraints do the real work — a stale row for the same
	// thumbprint OR container_id surfaces as a constraint violation
	// here and the handler chooses how to react (typically: alert +
	// evict by container_id + retry).
	_, err := r.db.Exec(`
		INSERT INTO agents (thumbprint_hex, container_id, agent_name, project, registered_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		tpHex,
		entry.ContainerID,
		entry.AgentName,
		entry.Project,
		entry.RegisteredAt.Unix(),
		entry.LastSeen.Unix(),
	)
	if err != nil {
		return fmt.Errorf("agentregistry: persist entry: %w", err)
	}

	r.mu.Lock()
	r.entries[entry.Thumbprint] = entry
	r.mu.Unlock()

	r.log.Info().
		Str("agent", entry.AgentName).
		Str("project", entry.Project).
		Str("container_id", entry.ContainerID).
		Msg("agentregistry: agent registered")
	return nil
}

func (r *sqliteRegistry) LookupByThumbprint(thumbprint [sha256.Size]byte) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[thumbprint]
	if !ok {
		return nil, ErrUnknownAgent
	}
	e := entry
	return &e, nil
}

func (r *sqliteRegistry) LookupByContainerID(containerID string) (*Entry, error) {
	if containerID == "" {
		return nil, ErrUnknownAgent
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.ContainerID == containerID {
			e := entry
			return &e, nil
		}
	}
	return nil, ErrUnknownAgent
}

func (r *sqliteRegistry) Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error) {
	r.mu.RLock()
	entry, ok := r.entries[thumbprint]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownAgent
	}
	want := auth.CanonicalAgentCN(auth.MustProjectSlug(entry.Project), auth.MustAgentName(entry.AgentName))
	if subtle.ConstantTimeCompare([]byte(cn), []byte(want)) != 1 {
		return nil, ErrUnknownAgent
	}
	return &entry, nil
}

func (r *sqliteRegistry) EvictByContainerID(containerID string) {
	if containerID == "" {
		return
	}
	res, err := r.db.Exec(`DELETE FROM agents WHERE container_id = ?`, containerID)
	if err != nil {
		// Best-effort eviction. Logged so an operator can see it but
		// the in-memory eviction still proceeds — leaving a stale row
		// in memory while the DB call fails would diverge mirror from
		// disk in the wrong direction.
		r.log.Error().
			Err(err).
			Str("container_id", containerID).
			Msg("agentregistry: sqlite delete failed; evicting from memory anyway")
	} else if rows, _ := res.RowsAffected(); rows > 0 {
		r.log.Info().
			Int64("rows", rows).
			Str("container_id", containerID).
			Msg("agentregistry: sqlite row evicted")
	}

	r.mu.Lock()
	var evicted []Entry
	for tp, entry := range r.entries {
		if entry.ContainerID == containerID {
			delete(r.entries, tp)
			evicted = append(evicted, entry)
		}
	}
	r.mu.Unlock()
	for _, entry := range evicted {
		r.log.Info().
			Str("agent", entry.AgentName).
			Str("project", entry.Project).
			Str("container_id", entry.ContainerID).
			Msg("agentregistry: agent evicted")
	}
}

func (r *sqliteRegistry) Snapshot() []Entry {
	r.mu.RLock()
	out := make([]Entry, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry)
	}
	r.mu.RUnlock()
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
