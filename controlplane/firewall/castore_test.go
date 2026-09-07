package firewall_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/firewall"
)

func newTestCAStore(t *testing.T, certDirFn func() (string, error)) *firewall.CAStore {
	t.Helper()
	store, err := firewall.NewCAStore(certDirFn)
	require.NoError(t, err)
	return store
}

func TestNewCAStore_RejectsNilCertDirFn(t *testing.T) {
	store, err := firewall.NewCAStore(nil)
	require.ErrorIs(t, err, firewall.ErrNilCACertDirFn)
	assert.Nil(t, store)
}

func TestCAStore_LoadDoesNotGenerate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	store := newTestCAStore(t, func() (string, error) { return dir, nil })
	_, _, err := store.Load()
	require.ErrorIs(t, err, firewall.ErrNoCA)
	assert.NoDirExists(t, dir)

	created, _, err := store.Ensure()
	require.NoError(t, err)
	loaded, key, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, created.SerialNumber, loaded.SerialNumber)
	_, _, err = firewall.GenerateSNICert(loaded, key, "probe.example.com")
	require.NoError(t, err)
}

func TestCAStore_LoadDuringRotate(t *testing.T) {
	const readers = 8
	dir := t.TempDir()
	store := newTestCAStore(t, func() (string, error) { return dir, nil })
	initial, _, err := store.Ensure()
	require.NoError(t, err)

	start := make(chan struct{})
	errors := make(chan error, readers+1)
	var workers sync.WaitGroup
	for range readers {
		workers.Go(func() {
			<-start
			if readErr := loadCAProbes(store, 200); readErr != nil {
				errors <- readErr
			}
		})
	}
	workers.Go(func() {
		<-start
		for range 30 {
			if rotateErr := store.Rotate(nil); rotateErr != nil {
				errors <- fmt.Errorf("rotate CA: %w", rotateErr)
				return
			}
		}
	})
	close(start)
	workers.Wait()
	close(errors)
	for workerErr := range errors {
		require.NoError(t, workerErr)
	}
	final, _, err := store.Load()
	require.NoError(t, err)
	assert.NotEqual(t, initial.SerialNumber, final.SerialNumber)
}

func loadCAProbes(store *firewall.CAStore, count int) error {
	for range count {
		cert, key, err := store.Load()
		if err != nil {
			return fmt.Errorf("load during rotation: %w", err)
		}
		if _, _, signErr := firewall.GenerateSNICert(cert, key, "probe.example.com"); signErr != nil {
			return fmt.Errorf("sign with loaded CA pair: %w", signErr)
		}
	}
	return nil
}
