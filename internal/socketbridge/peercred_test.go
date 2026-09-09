package socketbridge_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/socketbridge"
)

func TestReadListenerIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "listener.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})
	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		accepted <- connection.Close()
	}()

	identity, err := socketbridge.ReadListenerIdentity(path)

	require.NoError(t, err)
	assert.Equal(t, os.Getuid(), identity.UID)
	assert.Equal(t, os.Getgid(), identity.GID)
	require.NoError(t, <-accepted)
}

func TestReadListenerIdentityDeadSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	listener.SetUnlinkOnClose(false)
	require.NoError(t, listener.Close())
	t.Cleanup(func() {
		require.NoError(t, os.Remove(path))
	})

	_, err = socketbridge.ReadListenerIdentity(path)

	require.Error(t, err)
	assert.ErrorContains(t, err, path)
}
