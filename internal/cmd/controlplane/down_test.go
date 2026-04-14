package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func downOptsFrom(tb *testBed) *DownOptions {
	return &DownOptions{IOStreams: tb.F.IOStreams, ControlPlane: tb.F.ControlPlane}
}

func TestDownRun_ShortCircuitsWhenCPNotRunning(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return false, nil }
	// No StopFunc — a call would panic, which is the assertion that the
	// short-circuit holds.

	require.NoError(t, downRun(context.Background(), downOptsFrom(tb)))
	assert.Contains(t, tb.Stdout.String(), "not running")
	assert.Len(t, tb.Mock.StopCalls(), 0, "Stop must not be invoked when CP is absent")
}

func TestDownRun_StopsCPAndWarnsAboutFirewall(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return true, nil }
	tb.Mock.StopFunc = func(_ context.Context) error { return nil }

	require.NoError(t, downRun(context.Background(), downOptsFrom(tb)))
	assert.Len(t, tb.Mock.StopCalls(), 1, "Stop must be invoked when CP is running")
	assert.Contains(t, tb.Stdout.String(), "Control plane stopped")
	assert.Contains(t, tb.Stderr.String(), "Envoy and CoreDNS",
		"warning about orphan firewall containers is required")
	// Stream contract: the warning must go to stderr, not stdout.
	assert.NotContains(t, tb.Stdout.String(), "Envoy and CoreDNS",
		"orphan-firewall warning must not appear on stdout")
}

// Table-driven error propagation for both failure sites in downRun
// (IsRunning before Stop, Stop itself). Collapses what used to be two
// near-identical test bodies into a single loop.
func TestDownRun_PropagatesErrors(t *testing.T) {
	tb := newTestBed(t)
	isRunningErr := errors.New("docker: list failed")
	stopErr := errors.New("docker: stop failed")

	cases := []struct {
		name       string
		isRunning  bool
		runErr     error
		stopErr    error
		wantPrefix string
		wantWrap   error
	}{
		{
			name:       "IsRunning error",
			runErr:     isRunningErr,
			wantPrefix: "checking control plane",
			wantWrap:   isRunningErr,
		},
		{
			name:       "Stop error",
			isRunning:  true,
			stopErr:    stopErr,
			wantPrefix: "stopping control plane",
			wantWrap:   stopErr,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) {
				return tc.isRunning, tc.runErr
			}
			tb.Mock.StopFunc = func(_ context.Context) error {
				if tc.stopErr == nil && tc.runErr != nil {
					t.Fatalf("Stop must not be called when IsRunning errors")
				}
				return tc.stopErr
			}
			err := downRun(context.Background(), downOptsFrom(tb))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantPrefix)
			assert.ErrorIs(t, err, tc.wantWrap)
		})
	}
}

func TestNewCmdDown_RunFReceivesOptions(t *testing.T) {
	tb := newTestBed(t)
	called := false
	cmd := NewCmdDown(tb.F, func(_ context.Context, opts *DownOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		assert.NotNil(t, opts.ControlPlane)
		return nil
	})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	assert.True(t, called)
}
