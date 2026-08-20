package bridge

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/socketbridge"
)

// fakeEventsClient implements dockerEventsClient for testing.
type fakeEventsClient struct {
	messagesCh      chan events.Message
	errCh           chan error
	closed          atomic.Bool
	capturedOptions client.EventsListOptions
}

func TestLoadBridgedSockets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sockets.json")
	want := []socketbridge.BridgedSocket{{
		HostPath: "/host/service.sock",
		Target:   "/run/service.sock",
		Identity: socketbridge.ListenerIdentity{UID: 1000, GID: 1001},
		Group:    "service",
		Mode:     "0660",
	}}
	require.NoError(t, socketbridge.WriteBridgedSocketsFile(path, want))

	got, err := loadBridgedSockets(path)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want[0].HostPath, got[0].HostPath)
	assert.Equal(t, want[0].Target, got[0].Target)
	assert.Equal(t, want[0].Identity.UID, got[0].Identity.UID)
	assert.Equal(t, want[0].Identity.GID, got[0].Identity.GID)
	assert.Equal(t, want[0].Group, got[0].Group)
	assert.Equal(t, want[0].Mode, got[0].Mode)
}

func TestLoadBridgedSocketsAllowsNoFile(t *testing.T) {
	got, err := loadBridgedSockets("")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func newFakeEventsClient() *fakeEventsClient {
	return &fakeEventsClient{
		messagesCh: make(chan events.Message, 1),
		errCh:      make(chan error, 1),
	}
}

func (f *fakeEventsClient) Events(_ context.Context, options client.EventsListOptions) client.EventsResult {
	f.capturedOptions = options
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

	// Verify the events filter includes the expected container, type, and event
	filters := fake.capturedOptions.Filters
	assert.True(t, filters["type"][string(events.ContainerEventType)], "filter should include type=container")
	assert.True(t, filters["container"]["abc123"], "filter should include container=abc123")
	assert.True(t, filters["event"][string(events.ActionDie)], "filter should include event=die")
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
