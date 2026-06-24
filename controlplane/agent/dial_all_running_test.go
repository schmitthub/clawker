package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// newTestDialerForPoll builds a Dialer with only the fields
// DialAllRunning's poll loop touches (log + dialing map). The list-error
// path never reaches DialAgent's dial machinery, so cert/topic wiring is
// unnecessary.
func newTestDialerForPoll() *Dialer {
	return &Dialer{
		Log:     logger.Nop(),
		Dialing: make(map[string]context.CancelFunc),
	}
}

// TestDialAllRunning_RetriesListThenGivesUp confirms the initial-dial
// poll retries the docker list with bounded backoff and gives up after
// initialDialMaxAttempts without panicking or dialing — a permanently
// failing daemon must not strand the goroutine or crash CP.
func TestDialAllRunning_RetriesListThenGivesUp(t *testing.T) {
	var calls int32
	done := make(chan struct{})
	fake := &whailtest.FakeAPIClient{
		ContainerListFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			if atomic.AddInt32(&calls, 1) == int32(initialDialMaxAttempts) {
				close(done)
			}
			return mobyclient.ContainerListResult{}, errors.New("daemon down")
		},
	}
	lister := NewContainerLister(fake, configmocks.NewBlankConfig())

	newTestDialerForPoll().DialAllRunning(context.Background(), lister, ListOpts{})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DialAllRunning did not exhaust list retries in time")
	}
	// Give the goroutine a beat to return after the final attempt; no
	// further list calls may occur.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(initialDialMaxAttempts), atomic.LoadInt32(&calls),
		"list must be retried exactly initialDialMaxAttempts times")
}

// TestDialAllRunning_ListUsesProvidedOpts confirms the poll passes the
// caller's ListOpts straight through to the lister (the orchestrator
// dials running-only at boot via ListOpts{}).
func TestDialAllRunning_ListUsesProvidedOpts(t *testing.T) {
	var mu sync.Mutex
	var capturedAll bool
	done := make(chan struct{})
	fake := &whailtest.FakeAPIClient{
		ContainerListFn: func(_ context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			mu.Lock()
			capturedAll = opts.All
			mu.Unlock()
			close(done)
			// Empty list: success path, nothing to dial, no cert needed.
			return mobyclient.ContainerListResult{}, nil
		},
	}
	lister := NewContainerLister(fake, configmocks.NewBlankConfig())

	newTestDialerForPoll().DialAllRunning(context.Background(), lister, ListOpts{})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DialAllRunning did not list in time")
	}
	mu.Lock()
	defer mu.Unlock()
	require.False(t, capturedAll, "boot poll lists running-only when given ListOpts{}")
}
