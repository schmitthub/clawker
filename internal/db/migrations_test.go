package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestOpenAppliesEmbeddedGooseMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clawker-cli.db")

	database, err := db.Open(path, logger.Nop())
	require.NoError(t, err)

	inspection, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, inspection.Close()) })

	var tableName string
	require.NoError(t, inspection.QueryRowContext(
		t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'grants'`,
	).Scan(&tableName))
	assert.Equal(t, "grants", tableName)

	var applied int
	require.NoError(t, inspection.QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 1 AND is_applied = 1`,
	).Scan(&applied))
	assert.Equal(t, 1, applied)
	require.NoError(t, database.Close())

	database, err = db.Open(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, inspection.QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 1 AND is_applied = 1`,
	).Scan(&applied))
	assert.Equal(t, 1, applied)
}
