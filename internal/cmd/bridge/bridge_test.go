package bridge

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEventsClient implements dockerEventsClient for testing.
type fakeEventsClient struct {
	messagesCh chan events.Message
	errCh      chan error
	closed     atomic.Bool
}

func newFakeEventsClient() *fakeEventsClient {
	return &fakeEventsClient{
		messagesCh: make(chan events.Message, 1),
		errCh:      make(chan error, 1),
	}
}

func (f *fakeEventsClient) Events(_ context.Context, _ client.EventsListOptions) client.EventsResult {
	return client.EventsResult{
		Messages: f.messagesCh,
		Err:      f.errCh,
	}
}

func (f *fakeEventsClient) Close() error {
	f.closed.Store(true)
	return nil
}

func TestWatchContainerEvents_DieEvent(t *testing.T) {
	fake := newFakeEventsClient()
	ctx := context.Background()

	var deathCalled atomic.Bool

	// Send die event
	fake.messagesCh <- events.Message{
		Action: events.ActionDie,
	}

	err := watchContainerEvents(ctx, fake, "abc123", func() {
		deathCalled.Store(true)
	})

	require.NoError(t, err)
	assert.True(t, deathCalled.Load(), "onDeath should have been called")
	assert.True(t, fake.closed.Load(), "client should have been closed")
}

func TestWatchContainerEvents_StreamError(t *testing.T) {
	fake := newFakeEventsClient()
	ctx := context.Background()

	streamErr := fmt.Errorf("connection reset by peer")
	fake.errCh <- streamErr

	var deathCalled atomic.Bool
	err := watchContainerEvents(ctx, fake, "abc123", func() {
		deathCalled.Store(true)
	})

	require.Error(t, err)
	assert.Equal(t, streamErr, err)
	assert.False(t, deathCalled.Load(), "onDeath should NOT have been called on stream error")
	assert.True(t, fake.closed.Load(), "client should have been closed")
}

func TestWatchContainerEvents_ContextCancelled(t *testing.T) {
	fake := newFakeEventsClient()
	ctx, cancel := context.WithCancel(context.Background())

	var deathCalled atomic.Bool

	// Cancel context after a short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := watchContainerEvents(ctx, fake, "abc123", func() {
		deathCalled.Store(true)
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, deathCalled.Load(), "onDeath should NOT have been called on context cancel")
	assert.True(t, fake.closed.Load(), "client should have been closed")
}
