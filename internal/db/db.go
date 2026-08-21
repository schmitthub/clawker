// Package db owns the CLI database and its schema migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/schmitthub/clawker/internal/logger"
)

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
	migrateErr := database.migrate(context.Background())
	if migrateErr != nil {
		if closeErr := connection.Close(); closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("db: migrate schema: %w", migrateErr),
				fmt.Errorf("db: close after migration failure: %w", closeErr),
			)
		}
		return nil, fmt.Errorf("db: migrate schema: %w", migrateErr)
	}
	return database, nil
}

func (d *DB) migrate(ctx context.Context) error {
	var version int
	readErr := d.sql.QueryRowContext(ctx, readSchemaVersionSQL).Scan(&version)
	if readErr != nil {
		return fmt.Errorf("read schema version: %w", readErr)
	}
	switch version {
	case 0:
		return d.migrateFromZero(ctx)
	case schemaVersion:
		return nil
	default:
		return fmt.Errorf("unsupported schema version %d", version)
	}
}

func (d *DB) migrateFromZero(ctx context.Context) error {
	transaction, beginErr := d.sql.BeginTx(ctx, nil)
	if beginErr != nil {
		return fmt.Errorf("begin schema migration: %w", beginErr)
	}
	if _, createErr := transaction.ExecContext(ctx, createGrantsTableSQL); createErr != nil {
		return rollbackMigration(transaction, fmt.Errorf("create grants table: %w", createErr))
	}
	if _, versionErr := transaction.ExecContext(ctx, setSchemaVersionSQL); versionErr != nil {
		return rollbackMigration(transaction, fmt.Errorf("set schema version: %w", versionErr))
	}
	if commitErr := transaction.Commit(); commitErr != nil {
		return fmt.Errorf("commit schema migration: %w", commitErr)
	}
	return nil
}

func rollbackMigration(transaction *sql.Tx, migrationErr error) error {
	if rollbackErr := transaction.Rollback(); rollbackErr != nil {
		return errors.Join(migrationErr, fmt.Errorf("roll back migration: %w", rollbackErr))
	}
	return migrationErr
}

// Close closes the CLI database connection.
func (d *DB) Close() error {
	if closeErr := d.sql.Close(); closeErr != nil {
		return fmt.Errorf("db: close SQLite database: %w", closeErr)
	}
	return nil
}
