package controlplane

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// testBed bundles a wired Factory with its stdout/stderr capture buffers
// and the concrete ManagerMock so tests can program per-method responses
// without reaching into the Factory struct.
type testBed struct {
	F      *cmdutil.Factory
	Mock   *mocks.ManagerMock
	Stdout *bytes.Buffer
	Stderr *bytes.Buffer
}

// newTestBed returns a testBed with a fresh ManagerMock wired as
// f.ControlPlane. Every Manager method is nil by default — tests program
// the methods they exercise; unprogrammed methods panic, which is the
// failure signal we want for "this path shouldn't call X".
func newTestBed(t *testing.T) *testBed {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()
	mgr := &mocks.ManagerMock{}
	f := &cmdutil.Factory{
		IOStreams:    ios,
		ControlPlane: func() cpboot.Manager { return mgr },
	}
	return &testBed{F: f, Mock: mgr, Stdout: stdout, Stderr: stderr}
}

func upOptsFrom(tb *testBed) *UpOptions {
	return &UpOptions{IOStreams: tb.F.IOStreams, ControlPlane: tb.F.ControlPlane}
}

func TestUpRun_CallsEnsureRunningAndReportsSuccess(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.EnsureRunningFunc = func(_ context.Context) (time.Duration, error) { return 0, nil }

	require.NoError(t, upRun(context.Background(), upOptsFrom(tb)))
	assert.Len(t, tb.Mock.EnsureRunningCalls(), 1, "EnsureRunning must be invoked once")
	assert.Contains(t, tb.Stdout.String(), "Control plane is up")
}

func TestUpRun_WrapsEnsureRunningError(t *testing.T) {
	tb := newTestBed(t)
	bootErr := errors.New("healthz timed out")
	tb.Mock.EnsureRunningFunc = func(_ context.Context) (time.Duration, error) { return 0, bootErr }

	err := upRun(context.Background(), upOptsFrom(tb))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bringing control plane up")
	assert.ErrorIs(t, err, bootErr)
	assert.Empty(t, tb.Stdout.String(), "success line must not print on failure")
}

func TestNewCmdUp_RunFReceivesOptions(t *testing.T) {
	tb := newTestBed(t)
	called := false
	cmd := NewCmdUp(tb.F, func(_ context.Context, opts *UpOptions) error {
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
