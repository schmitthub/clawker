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

// DB is the CLI database connection and its domain stores.
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

	connection, err := sql.Open(sqliteDriverName, path+sqliteDSNSuffix)
	if err != nil {
		return nil, fmt.Errorf("db: open SQLite database: %w", err)
	}
	connection.SetMaxOpenConns(1)
	database := &DB{sql: connection, log: log}
	if err := database.migrate(context.Background()); err != nil {
		if closeErr := connection.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("db: migrate schema: %w", err), fmt.Errorf("db: close after migration failure: %w", closeErr))
		}
		return nil, fmt.Errorf("db: migrate schema: %w", err)
	}
	return database, nil
}

func (d *DB) migrate(ctx context.Context) error {
	var version int
	if err := d.sql.QueryRowContext(ctx, readSchemaVersionSQL).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	switch version {
	case 0:
		transaction, err := d.sql.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema migration: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, createGrantsTableSQL); err != nil {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				return errors.Join(fmt.Errorf("create grants table: %w", err), fmt.Errorf("roll back migration: %w", rollbackErr))
			}
			return fmt.Errorf("create grants table: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, setSchemaVersionSQL); err != nil {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil {
				return errors.Join(fmt.Errorf("set schema version: %w", err), fmt.Errorf("roll back migration: %w", rollbackErr))
			}
			return fmt.Errorf("set schema version: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit schema migration: %w", err)
		}
	case schemaVersion:
		return nil
	default:
		return fmt.Errorf("unsupported schema version %d", version)
	}
	return nil
}

// Close closes the CLI database connection.
func (d *DB) Close() error {
	if err := d.sql.Close(); err != nil {
		return fmt.Errorf("db: close SQLite database: %w", err)
	}
	return nil
}
