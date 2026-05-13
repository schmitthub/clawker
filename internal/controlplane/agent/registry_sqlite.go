package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/logger"
)

// migrationsFS embeds the goose-format SQL migration files. Co-located
// with the registry so the schema definitions ship inside the CP binary
// — the CP container has no bind mount for source assets, so loose .sql
// files on the host filesystem would not be reachable at runtime.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

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
// process. CP is the SOLE writer in this design (the CLI no longer
// opens the registry DB), so a separate reader-mode constructor is
// not provided — every consumer that previously read from sqlite now
// goes through `f.AdminClient(ctx).ListAgents`.
//
// The parent directory of dbPath must already exist.
func NewSQLiteWriter(dbPath string, log *logger.Logger) (Registry, error) {
	return openSQLite(dbPath, log)
}

// EnsureSchema opens the database in writer mode (creating the file
// if missing), applies the schema, and closes. Called by the CP
// daemon at startup before NewSQLiteWriter so the schema apply path
// is observable independent of the long-lived writer handle.
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

func openSQLite(dbPath string, log *logger.Logger) (Registry, error) {
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
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: open sqlite: %w", err)
	}
	// Single writer connection. modernc/sqlite is goroutine-safe, but
	// a single connection serialises writes which matches the
	// single-writer model the design assumes.
	db.SetMaxOpenConns(1)
	if err := applySchema(context.Background(), db, log); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("agentregistry: apply schema: %w", err)
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
		Int("rows", rowCount).
		Msg("agentregistry: sqlite registry opened")
	return r, nil
}

// applySchema runs every embedded goose migration through goose.Up.
// goose tracks applied versions in its own `goose_db_version` table,
// so re-running on an up-to-date DB is a no-op. Migrations live in
// migrations/*.sql co-located with this file and are baked into the
// binary via migrationsFS (see CP container has no host-side asset
// bind mount).
func applySchema(ctx context.Context, db *sql.DB, log *logger.Logger) error {
	if err := migrateFromPreGoose(ctx, db, log); err != nil {
		return fmt.Errorf("agentregistry: pre-goose cleanup: %w", err)
	}
	subFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("agentregistry: sub migrations FS: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, subFS,
		goose.WithLogger(&gooseLoggerAdapter{log: log}),
	)
	if err != nil {
		return fmt.Errorf("agentregistry: build goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("agentregistry: apply migrations: %w", err)
	}
	for _, r := range results {
		log.Info().
			Int64("version", r.Source.Version).
			Str("file", r.Source.Path).
			Msg("agentregistry: migration applied")
	}
	return nil
}

// migrateFromPreGoose handles the one-time transition from the
// hand-rolled `agents` table (no goose_db_version sibling) to a
// goose-managed schema. If the DB has an `agents` table but no
// `goose_db_version`, the table is dropped — alpha policy: registry
// state is regenerable from the next clawker run's Register handshake,
// and adopting goose mid-life-of-a-DB by faking a baseline version is
// fragile across future schema changes. After this point, goose owns
// migration state exclusively.
func migrateFromPreGoose(ctx context.Context, db *sql.DB, log *logger.Logger) error {
	var hasGoose int
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version')`,
	).Scan(&hasGoose)
	if err != nil {
		return fmt.Errorf("inspect goose_db_version: %w", err)
	}
	if hasGoose == 1 {
		return nil
	}
	var hasAgents int
	err = db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='agents')`,
	).Scan(&hasAgents)
	if err != nil {
		return fmt.Errorf("inspect agents table: %w", err)
	}
	if hasAgents == 1 {
		log.Warn().Msg("agentregistry: pre-goose schema detected; dropping agents table (alpha policy — agents will re-Register on next clawker run)")
		if _, err := db.ExecContext(ctx, `DROP TABLE agents`); err != nil {
			return fmt.Errorf("drop pre-goose agents: %w", err)
		}
	}
	return nil
}

// gooseLoggerAdapter routes goose's internal Printf/Fatalf calls into
// the project zerolog. goose's interface predates structured logging;
// the format strings are passed through opaquely.
type gooseLoggerAdapter struct {
	log *logger.Logger
}

func (g *gooseLoggerAdapter) Printf(format string, v ...any) {
	g.log.Info().Msgf("goose: "+format, v...)
}

func (g *gooseLoggerAdapter) Fatalf(format string, v ...any) {
	// goose never calls Fatalf in NewProvider/Up paths we use; if a
	// future goose version changes that, demote to Error rather than
	// honouring the Fatal semantic — this is on the CP boot path and
	// os.Exit would strand eBPF.
	g.log.Error().Msgf("goose (would-be-fatal): "+format, v...)
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
