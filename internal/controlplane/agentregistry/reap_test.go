package agentregistry

import (
	"context"
	"errors"
	"sync/atomic"
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

func TestReap_RetriesOnTransientListerError(t *testing.T) {
	// First two lister calls fail with a transient error; third
	// succeeds. Reap must complete and evict the orphan row from the
	// successful attempt — a single transient docker-daemon hiccup at
	// CP startup must not skip the first sweep entirely.
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-keep", "cert-a"))
	mustAdd(t, r, validEntry("", "b", "ctr-orphan", "cert-b"))

	var attempts atomic.Int32
	lister := func(_ context.Context) ([]string, error) {
		n := attempts.Add(1)
		if n < 3 {
			return nil, errors.New("daemon temporarily unavailable")
		}
		return []string{"ctr-keep"}, nil
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, evicted)
	assert.EqualValues(t, 3, attempts.Load(), "lister must be retried up to maxAttempts")
}

// flakyEvictRegistry wraps an in-memory Registry but injects an
// Evict failure for a configured set of container_ids — used by the
// partial-evict aggregation test below to drive Reap into the
// "some succeed, some fail" path that the production sqlite backend
// hits on row-level constraint failures.
type flakyEvictRegistry struct {
	Registry
	failOn map[string]error
	calls  []string
}

func (f *flakyEvictRegistry) EvictByContainerID(id string) error {
	f.calls = append(f.calls, id)
	if err, ok := f.failOn[id]; ok {
		return err
	}
	return f.Registry.EvictByContainerID(id)
}

func TestReap_PartialEvict_AggregatesErrorsAndCountsSuccesses(t *testing.T) {
	// Three orphan rows; Evict succeeds for two and fails for one. Reap
	// must NOT short-circuit on the first failure — every row gets its
	// own attempt, the count reflects the successes, and the returned
	// error joins all failures so the caller can surface them.
	inner := NewRegistry(nil)
	mustAdd(t, inner, validEntry("", "a", "ctr-orphan-1", "cert-a"))
	mustAdd(t, inner, validEntry("", "b", "ctr-orphan-2", "cert-b"))
	mustAdd(t, inner, validEntry("", "c", "ctr-orphan-3", "cert-c"))

	flaky := &flakyEvictRegistry{
		Registry: inner,
		failOn:   map[string]error{"ctr-orphan-2": errors.New("disk full")},
	}

	lister := func(_ context.Context) ([]string, error) { return nil, nil }

	evicted, err := Reap(context.Background(), flaky, lister, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ctr-orphan-2")
	assert.Contains(t, err.Error(), "disk full")
	assert.Equal(t, 2, evicted, "successful evicts must count even when peers fail")
	assert.ElementsMatch(t, []string{"ctr-orphan-1", "ctr-orphan-2", "ctr-orphan-3"}, flaky.calls,
		"every orphan must get an evict attempt; no short-circuit on first failure")
}

func TestReap_GivesUpAfterMaxRetries(t *testing.T) {
	// Lister fails on every attempt. Reap must return the last error
	// and evict nothing.
	r := NewRegistry(nil)
	mustAdd(t, r, validEntry("", "a", "ctr-1", "cert-a"))

	var attempts atomic.Int32
	lister := func(_ context.Context) ([]string, error) {
		attempts.Add(1)
		return nil, errors.New("daemon down")
	}

	evicted, err := Reap(context.Background(), r, lister, nil)
	require.Error(t, err)
	assert.Equal(t, 0, evicted)
	assert.EqualValues(t, 3, attempts.Load(), "lister retried up to the bounded ceiling")

	// Row preserved.
	_, err = r.Lookup(tp("cert-a"), canonical("", "a"))
	assert.NoError(t, err)
}
