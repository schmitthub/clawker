// Package db owns the CLI database and its schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/logger"
)

// migrationsFS contains the Goose SQL migration files.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB is the CLI database connection.
type DB struct {
	sql *sql.DB
	log *logger.Logger
}

// Open opens the CLI database at path and applies its schema migrations.
func Open(path string, log *logger.Logger) (*DB, error) {
	if path == "" {
		return nil, errors.New("db: path is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err != nil {
		return nil, fmt.Errorf("db: parent directory %s is not available: %w", parent, err)
	}

	connection, openErr := sql.Open(sqliteDriverName, path+sqliteDSNSuffix)
	if openErr != nil {
		return nil, fmt.Errorf("db: open SQLite database: %w", openErr)
	}
	connection.SetMaxOpenConns(1)
	database := &DB{sql: connection, log: log}
	migrateErr := applyMigrations(context.Background(), database.sql, database.log)
	if migrateErr != nil {
		return nil, closeAfterOpenFailure(connection, fmt.Errorf("db: migrate schema: %w", migrateErr))
	}
	if permissionErr := tightenSQLiteFiles(path); permissionErr != nil {
		return nil, closeAfterOpenFailure(connection, permissionErr)
	}
	return database, nil
}

func tightenSQLiteFiles(path string) error {
	for _, suffix := range []string{"", sqliteWALSuffix, sqliteSHMSuffix} {
		if chmodErr := os.Chmod(path+suffix, sqliteFileMode); chmodErr != nil {
			if suffix != "" && errors.Is(chmodErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("db: tighten SQLite file %s: %w", path+suffix, chmodErr)
		}
	}
	return nil
}

func closeAfterOpenFailure(connection *sql.DB, openErr error) error {
	if closeErr := connection.Close(); closeErr != nil {
		return errors.Join(openErr, fmt.Errorf("db: close after open failure: %w", closeErr))
	}
	return openErr
}

func applyMigrations(ctx context.Context, connection *sql.DB, log *logger.Logger) error {
	subFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration file system: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		connection,
		subFS,
		goose.WithLogger(&gooseLoggerAdapter{log: log}),
	)
	if err != nil {
		return fmt.Errorf("create Goose provider: %w", err)
	}
	results, upErr := provider.Up(ctx)
	for _, result := range results {
		log.Info().
			Int64("version", result.Source.Version).
			Str("file", result.Source.Path).
			Msg("db: migration applied")
	}
	if upErr != nil {
		return fmt.Errorf("apply Goose migrations: %w", upErr)
	}
	return nil
}

type gooseLoggerAdapter struct {
	log *logger.Logger
}

func (g *gooseLoggerAdapter) Printf(format string, values ...any) {
	g.log.Info().Msgf("goose: "+format, values...)
}

func (g *gooseLoggerAdapter) Fatalf(format string, values ...any) {
	g.log.Error().Msgf("goose: "+format, values...)
}

// Close closes the CLI database connection.
func (d *DB) Close() error {
	if closeErr := d.sql.Close(); closeErr != nil {
		return fmt.Errorf("db: close SQLite database: %w", closeErr)
	}
	return nil
}
