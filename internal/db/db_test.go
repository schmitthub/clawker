package db_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestOpenTightensSQLiteFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission modes")
	}
	path := filepath.Join(t.TempDir(), "clawker-cli.db")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	database, err := db.Open(path, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, statErr := os.Stat(path + suffix)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path+suffix)
	}
}
