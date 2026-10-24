package firewall_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	mobyevents "github.com/moby/moby/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestEnrollmentStore_RoundTrip covers the file-backed store the CP keeps
// beside the route identity table: a record the CLI pushed on FirewallEnable
// is durable across a reopen (the CP restart replay reads it), a later
// enable overwrites it, and an evict removes it. A container with no record
// reports found=false, which the handler treats as "no bypass".
func TestEnrollmentStore_RoundTrip(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := firewall.NewEnrollmentStore(cfg)
	require.NoError(t, err)

	const cid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	_, found, err := store.HostProxy(cid)
	require.NoError(t, err)
	assert.False(t, found, "fresh store holds no record")

	require.NoError(t, store.SetHostProxy(cid, true))
	enabled, found, err := store.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, enabled)

	// Reopen from disk: the record is what a restarted CP replays.
	reopened, err := firewall.NewEnrollmentStore(cfg)
	require.NoError(t, err)
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found, "record must survive a reopen")
	assert.True(t, enabled)

	// A later enable with the lane disabled overwrites the record.
	require.NoError(t, reopened.SetHostProxy(cid, false))
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, enabled)

	// Other containers are independent records.
	const other = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	require.NoError(t, reopened.SetHostProxy(other, true))
	enabled, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, enabled, "a write for another container must not touch this record")

	require.NoError(t, reopened.Remove(cid))
	_, found, err = reopened.HostProxy(cid)
	require.NoError(t, err)
	assert.False(t, found, "evicted record must be gone")
	_, found, err = reopened.HostProxy(other)
	require.NoError(t, err)
	assert.True(t, found, "removing one record must leave the others")

	// Removing an absent record is a no-op: a destroy event can arrive for
	// a container that never enrolled.
	require.NoError(t, reopened.Remove(cid))

	// The removal reached disk.
	dataDir, err := cfg.FirewallDataSubdir()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(dataDir, consts.FirewallEnrollmentsFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), cid)
	assert.Contains(t, string(raw), other)
}

// TestSubscribeEnrollmentEvict pins the eviction filter: container/destroy
// removes the record; stop/die/kill leave it, because a stopped container
// can be started again and its record must still replay.
func TestSubscribeEnrollmentEvict(t *testing.T) {
	cases := []struct {
		name        string
		eventType   mobyevents.Type
		action      mobyevents.Action
		wantEvicted bool
	}{
		{"destroy evicts", mobyevents.ContainerEventType, mobyevents.ActionDestroy, true},
		{"stop does not evict", mobyevents.ContainerEventType, mobyevents.ActionStop, false},
		{"die does not evict", mobyevents.ContainerEventType, mobyevents.ActionDie, false},
		{"image destroy does not evict", mobyevents.ImageEventType, mobyevents.ActionDestroy, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := configmocks.NewIsolatedTestConfig(t)
			store, err := firewall.NewEnrollmentStore(cfg)
			require.NoError(t, err)
			const cid = "ctr-target"
			require.NoError(t, store.SetHostProxy(cid, true))
			// The sentinel is destroyed after the event under test; the
			// subscriber queue is FIFO, so once the sentinel is gone the
			// event under test has been handled.
			const sentinel = "ctr-sentinel"
			require.NoError(t, store.SetHostProxy(sentinel, true))

			topic, err := pubsub.NewTopic[dockerevents.DockerEvent](logger.Nop())
			require.NoError(t, err)
			t.Cleanup(func() { _ = topic.Close() })
			firewall.SubscribeEnrollmentEvict(t.Context(), topic, store, logger.Nop())

			publish := func(eventType mobyevents.Type, action mobyevents.Action, id string) {
				var msg mobyevents.Message
				msg.Type = eventType
				msg.Action = action
				msg.Actor.ID = id
				var evt pubsub.Event[dockerevents.DockerEvent]
				evt.Payload = dockerevents.DockerEvent{Message: msg}
				topic.Publish(evt)
			}
			publish(tc.eventType, tc.action, cid)
			publish(mobyevents.ContainerEventType, mobyevents.ActionDestroy, sentinel)
			require.Eventually(t, func() bool {
				_, found, readErr := store.HostProxy(sentinel)
				return readErr == nil && !found
			}, 500*time.Millisecond, 10*time.Millisecond)

			_, found, err := store.HostProxy(cid)
			require.NoError(t, err)
			assert.Equal(t, !tc.wantEvicted, found, "record after a %s/%s event", tc.eventType, tc.action)
		})
	}
}
