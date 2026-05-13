package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/auth"
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
	results, upErr := provider.Up(ctx)
	// Log every applied migration regardless of whether Up returned an
	// error: on partial failure the operator needs to see the last
	// successful version to triage a stuck DB.
	for _, r := range results {
		log.Info().
			Int64("version", r.Source.Version).
			Str("file", r.Source.Path).
			Msg("agentregistry: migration applied")
	}
	if upErr != nil {
		return fmt.Errorf("agentregistry: apply migrations: %w", upErr)
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
	if err := validateEntry(entry); err != nil {
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
	_, err := r.db.Exec(`
		INSERT INTO agents (thumbprint_hex, container_id, agent_name, project, registered_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		tpHex,
		entry.ContainerID,
		entry.AgentName.String(),
		entry.Project.String(),
		entry.RegisteredAt.Unix(),
		entry.LastSeen.Unix(),
	)
	if err != nil {
		return fmt.Errorf("agentregistry: persist entry: %w", err)
	}

	r.log.Info().
		Str("agent", entry.AgentName.String()).
		Str("project", entry.Project.String()).
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
		return Entry{}, fmt.Errorf("%w: malformed thumbprint_hex %q", ErrMalformedEntry, tpHex)
	}
	copy(tp[:], raw)
	// Re-validate agent_name / project at read time. The typed fields
	// guarantee the strings WERE valid at write time, but a hand-edited
	// DB row (or a future writer that bypasses the typed boundary)
	// could otherwise land an invariant-violating value here. Wrap
	// ErrMalformedEntry so LookupByContainerID + Snapshot callers can
	// classify the failure mode (Register evicts the row and re-writes
	// using the middleware-resolved identity; Snapshot logs + skips).
	agentTyped, err := auth.NewAgentName(agentName)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: agent_name %q: %v", ErrMalformedEntry, agentName, err)
	}
	projectTyped, err := auth.NewProjectSlug(project)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: project %q: %v", ErrMalformedEntry, project, err)
	}
	return Entry{
		AgentName:    agentTyped,
		Project:      projectTyped,
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

func (r *sqliteRegistry) Snapshot() ([]Entry, error) {
	rows, err := r.db.Query(`SELECT ` + selectEntryCols + ` FROM agents`)
	if err != nil {
		r.log.Error().Err(err).Msg("agentregistry: query snapshot failed")
		return nil, fmt.Errorf("agentregistry: query snapshot: %w", err)
	}
	defer rows.Close()

	var (
		out         []Entry
		skippedRows int
	)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			skippedRows++
			r.log.Error().
				Err(err).
				Str("event", "agentregistry_row_skipped").
				Msg("agentregistry: skipping malformed row in Snapshot")
			continue
		}
		out = append(out, e)
	}
	if skippedRows > 0 {
		r.log.Warn().
			Int("skipped_rows", skippedRows).
			Str("event", "agentregistry_snapshot_skipped_rows").
			Msg("agentregistry: Snapshot omitted malformed rows — listed agents may be incomplete")
	}
	if err := rows.Err(); err != nil {
		r.log.Error().Err(err).Msg("agentregistry: snapshot rows iteration failed")
		return nil, fmt.Errorf("agentregistry: iterate snapshot rows: %w", err)
	}
	sortEntries(out)
	return out, nil
}

// Close releases the underlying sql.DB handle. Used by tests; the CP
// daemon holds the registry for its full process lifetime so there is
// no production caller for Close today.
func (r *sqliteRegistry) Close() error { return r.db.Close() }
