package cmdutil_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestSocketGrantStoreBuildsStoreFromFactoryNouns(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "clawker-cli.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	var factory cmdutil.Factory
	factory.DB = func() (*db.DB, error) { return database, nil }
	factory.Logger = func() (*logger.Logger, error) { return logger.Nop(), nil }

	store, err := cmdutil.SocketGrantStore(&factory)()

	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestSocketGrantStoreReturnsFactoryErrors(t *testing.T) {
	databaseErr := errors.New("database failed")
	loggerErr := errors.New("logger failed")

	var factory cmdutil.Factory
	factory.DB = func() (*db.DB, error) { return nil, databaseErr }
	_, err := cmdutil.SocketGrantStore(&factory)()
	require.ErrorIs(t, err, databaseErr)

	database, err := db.Open(filepath.Join(t.TempDir(), "clawker-cli.db"), logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	factory.DB = func() (*db.DB, error) { return database, nil }
	factory.Logger = func() (*logger.Logger, error) { return nil, loggerErr }
	_, err = cmdutil.SocketGrantStore(&factory)()
	require.ErrorIs(t, err, loggerErr)
}
