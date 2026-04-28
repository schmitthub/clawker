package agentregistry

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReap_EvictsRowsWithMissingContainers(t *testing.T) {
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-keep", "cert-a"))
	mustAdd(t, r, validEntry("", "b", "ctr-orphan", "cert-b"))

	lister := func(_ context.Context) ([]string, error) {
		return []string{"ctr-keep"}, nil
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, evicted)

	_, err = r.Lookup(tp("cert-a"), canonical("", "a"))
	assert.NoError(t, err, "live container's row must survive")

	_, err = r.Lookup(tp("cert-b"), canonical("", "b"))
	assert.ErrorIs(t, err, ErrUnknownAgent, "orphan row must be evicted")
}

func TestReap_NoOrphans_NoOp(t *testing.T) {
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-1", "cert-a"))
	mustAdd(t, r, validEntry("", "b", "ctr-2", "cert-b"))

	lister := func(_ context.Context) ([]string, error) {
		return []string{"ctr-1", "ctr-2"}, nil
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, evicted)
}

func TestReap_EmptyRegistry_NoOp(t *testing.T) {
	r := NewRegistry(nil)

	lister := func(_ context.Context) ([]string, error) {
		return []string{"ctr-1"}, nil
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, evicted)
}

func TestReap_EmptyContainerList_EvictsAll(t *testing.T) {
	// Every container was `docker rm`'d while CP was down. Reap must
	// clear the registry entirely — orphan rows would otherwise keep
	// authorizing per-agent RPCs against thumbprints that no longer
	// have a container behind them.
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-1", "cert-a"))
	mustAdd(t, r, validEntry("", "b", "ctr-2", "cert-b"))

	lister := func(_ context.Context) ([]string, error) {
		return nil, nil
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, evicted)
}

func TestReap_ListerError_PropagatesAndReapsNothing(t *testing.T) {
	// Transient docker daemon failure. Reap must NOT evict — a
	// best-effort eviction with a partial list would silently drop
	// legitimate rows. Caller logs the warning and proceeds; the
	// dockerevents subscription handles the steady-state case.
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-1", "cert-a"))

	lister := func(_ context.Context) ([]string, error) {
		return nil, errors.New("daemon unavailable")
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon unavailable")
	assert.Equal(t, 0, evicted)

	// Row must still be present.
	_, err = r.Lookup(tp("cert-a"), canonical("", "a"))
	assert.NoError(t, err)
}

func TestReap_NilRegistry_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil registry")
		}
	}()
	_, _ = Reap(context.Background(), nil, func(context.Context) ([]string, error) { return nil, nil }, nil)
}

func TestReap_NilLister_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil lister")
		}
	}()
	_, _ = Reap(context.Background(), NewRegistry(nil), nil, nil)
}
