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

// TestWaitForCPClockSync_WithinTolerance_ReturnsImmediately: a single
// in-tolerance probe passes the gate without re-polling.
func TestWaitForCPClockSync_WithinTolerance_ReturnsImmediately(t *testing.T) {
	orig := probeSkewFn
	t.Cleanup(func() { probeSkewFn = orig })

	var n atomic.Int32
	probeSkewFn = func(_ context.Context, _ int) (time.Duration, error) {
		n.Add(1)
		return cpClockSkewTolerance - time.Millisecond, nil
	}

	require.NoError(t, waitForCPClockSync(t.Context(), configmocks.NewBlankConfig()))
	assert.Equal(t, int32(1), n.Load(), "in-tolerance offset should pass on first probe")
}

// TestWaitForCPClockSync_ConvergesAfterDrift: an offset over tolerance
// re-polls until the (freshly-resynced) clock lands within tolerance.
func TestWaitForCPClockSync_ConvergesAfterDrift(t *testing.T) {
	orig := probeSkewFn
	t.Cleanup(func() { probeSkewFn = orig })

	var n atomic.Int32
	probeSkewFn = func(_ context.Context, _ int) (time.Duration, error) {
		if n.Add(1) < 2 {
			return 10 * time.Second, nil // VM clock still lagging
		}
		return 100 * time.Millisecond, nil // NTP caught up
	}

	require.NoError(t, waitForCPClockSync(t.Context(), configmocks.NewBlankConfig()))
	assert.GreaterOrEqual(t, n.Load(), int32(2), "must re-probe until within tolerance")
}

// TestWaitForCPClockSync_NonConvergence_ReturnsError: a chronically
// skewed clock (either direction — the gate compares magnitude) must
// fail rather than hang, so create aborts before minting poisoned
// bootstrap material.
func TestWaitForCPClockSync_NonConvergence_ReturnsError(t *testing.T) {
	orig := probeSkewFn
	t.Cleanup(func() { probeSkewFn = orig })

	probeSkewFn = func(_ context.Context, _ int) (time.Duration, error) {
		return -30 * time.Second, nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	require.Error(t, waitForCPClockSync(ctx, configmocks.NewBlankConfig()))
}

// TestNewCPClockSyncTimeout_BranchesOnMeasured: the unmeasured branch
// wraps the last probe error (so callers can errors.Is the root cause of
// a probe that never answered); the measured branch is a standalone
// actionable error that must NOT wrap the (now-stale) probe error.
func TestNewCPClockSyncTimeout_BranchesOnMeasured(t *testing.T) {
	boom := errors.New("probe boom")

	unmeasured := newCPClockSyncTimeout(time.Now(), 0, false, boom)
	assert.ErrorIs(t, unmeasured, boom, "unmeasured path must wrap the last probe error")

	measured := newCPClockSyncTimeout(time.Now().Add(-time.Second), 30*time.Second, true, boom)
	assert.NotErrorIs(t, measured, boom, "measured path is standalone, must not wrap a stale probe error")

	// Defensive nil-guard: the unmeasured path is unreachable in waitForCPClockSync
	// without a recorded probe error, but a nil lastErr must never %w-wrap into a
	// "%!w(<nil>)" cause — it must produce a clean, non-wrapping error.
	nilCause := newCPClockSyncTimeout(time.Now(), 0, false, nil)
	require.Error(t, nilCause, "unmeasured path must still return an error when lastErr is nil")
	assert.Nil(t, errors.Unwrap(nilCause), "nil lastErr must not be wrapped (no %!w(<nil>) cause)")
	assert.NotContains(t, nilCause.Error(), "<nil>", "rendered error must not leak a nil cause")
}
