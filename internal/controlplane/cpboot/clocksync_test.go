package cpboot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
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

	err := waitForCPClockSync(t.Context(), configmocks.NewBlankConfig(), logger.Nop())
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

	err := waitForCPClockSync(t.Context(), configmocks.NewBlankConfig(), logger.Nop())
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
	err := waitForCPClockSync(ctx, configmocks.NewBlankConfig(), logger.Nop())
	require.Error(t, err)
}
