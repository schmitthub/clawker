package cpboot

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// TestWaitForCPClockSync_CaughtUp_ReturnsImmediately: a single probe whose
// CP time is at/ahead of the host passes the gate without re-polling.
func TestWaitForCPClockSync_CaughtUp_ReturnsImmediately(t *testing.T) {
	orig := probeCPTimeFn
	t.Cleanup(func() { probeCPTimeFn = orig })

	var n atomic.Int32
	probeCPTimeFn = func(_ context.Context, _ int) (time.Time, error) {
		n.Add(1)
		return time.Now().UTC().Add(time.Second), nil // CP caught up to host
	}

	err := waitForCPClockSync(t.Context(), configmocks.NewBlankConfig())
	require.NoError(t, err)
	assert.Equal(t, int32(1), n.Load(), "a caught-up CP clock should pass on first probe")
}

// TestWaitForCPClockSync_ConvergesAfterDrift: a CP clock behind the host
// re-polls until the (freshly-resynced) clock catches up to the host.
func TestWaitForCPClockSync_ConvergesAfterDrift(t *testing.T) {
	orig := probeCPTimeFn
	t.Cleanup(func() { probeCPTimeFn = orig })

	var n atomic.Int32
	probeCPTimeFn = func(_ context.Context, _ int) (time.Time, error) {
		if n.Add(1) < 2 {
			return time.Now().UTC().Add(-10 * time.Second), nil // VM clock still lagging
		}
		return time.Now().UTC().Add(time.Second), nil // NTP caught up
	}

	err := waitForCPClockSync(t.Context(), configmocks.NewBlankConfig())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n.Load(), int32(2), "must re-probe until the CP catches up to the host")
}

// TestWaitForCPClockSync_NonConvergence_ReturnsError: a CP clock that stays
// behind the host must fail rather than hang, so create aborts before
// minting poisoned bootstrap material.
func TestWaitForCPClockSync_NonConvergence_ReturnsError(t *testing.T) {
	orig := probeCPTimeFn
	t.Cleanup(func() { probeCPTimeFn = orig })

	probeCPTimeFn = func(_ context.Context, _ int) (time.Time, error) {
		return time.Now().UTC().Add(-30 * time.Second), nil // CP perpetually behind host
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err := waitForCPClockSync(ctx, configmocks.NewBlankConfig())
	require.Error(t, err)
}

// TestNewCPClockSyncTimeout_WrapsCause: the timeout error wraps the last
// probe failure (so callers can errors.Is the root cause), and a nil cause
// must not %w-wrap into a "%!w(<nil>)" rendering.
func TestNewCPClockSyncTimeout_WrapsCause(t *testing.T) {
	boom := errors.New("probe boom")

	withCause := newCPClockSyncTimeout(time.Now().UTC().Add(-time.Second), boom)
	assert.ErrorIs(t, withCause, boom, "timeout must wrap the last probe error")

	// Defensive nil-guard: the loop only reaches the deadline after a probe,
	// but a nil lastErr must never %w-wrap into a "%!w(<nil>)" cause.
	nilCause := newCPClockSyncTimeout(time.Now(), nil)
	require.Error(t, nilCause, "must still return an error when lastErr is nil")
	assert.Nil(t, errors.Unwrap(nilCause), "nil lastErr must not be wrapped (no %!w(<nil>) cause)")
	assert.NotContains(t, nilCause.Error(), "<nil>", "rendered error must not leak a nil cause")
}
