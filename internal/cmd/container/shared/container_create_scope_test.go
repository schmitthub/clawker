package shared

import (
	"context"
	"fmt"
	"testing"

	moby "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// callIndex returns the position of the first call to method in the fake's
// ordered call log, or -1 if absent.
func callIndex(calls []string, method string) int {
	for i, c := range calls {
		if c == method {
			return i
		}
	}
	return -1
}

// TestCreateScope_Reclaim_RemovesContainerBeforeVolumes pins the ordering
// invariant: the container is removed first (which frees its volumes) and only
// then the newly-created volumes, because the Docker daemon refuses to delete a
// volume still referenced by a container. Regression guard for the create-time
// atomic teardown — a reorder, or skipping volume cleanup entirely, fails here.
func TestCreateScope_Reclaim_RemovesContainerBeforeVolumes(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerRemove()

	var removed []string
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, id string, _ moby.VolumeRemoveOptions) (moby.VolumeRemoveResult, error) {
		removed = append(removed, id)
		return moby.VolumeRemoveResult{}, nil
	}

	scope := &createScope{
		client:      fake.Client,
		log:         logger.Nop(),
		containerID: "cid-123",
		volumes:     []string{"vol-a", "vol-b"},
	}
	scope.reclaim()

	fake.AssertCalled(t, "ContainerRemove")
	require.Equal(t, []string{"vol-a", "vol-b"}, removed,
		"all newly-created volumes must be reclaimed, not orphaned")

	ci := callIndex(fake.FakeAPI.Calls, "ContainerRemove")
	vi := callIndex(fake.FakeAPI.Calls, "VolumeRemove")
	require.GreaterOrEqual(t, ci, 0, "ContainerRemove must be called")
	require.GreaterOrEqual(t, vi, 0, "VolumeRemove must be called")
	require.Less(t, ci, vi,
		"container must be removed before any volume (Docker refuses in-use volume deletion)")
}

// TestCreateScope_Reclaim_BestEffort verifies a failed volume removal does not
// abort the remaining cleanups — every tracked resource still gets an attempt.
func TestCreateScope_Reclaim_BestEffort(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerRemove()

	var removed []string
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, id string, _ moby.VolumeRemoveOptions) (moby.VolumeRemoveResult, error) {
		removed = append(removed, id)
		if id == "vol-a" {
			return moby.VolumeRemoveResult{}, fmt.Errorf("simulated in-use volume")
		}
		return moby.VolumeRemoveResult{}, nil
	}

	scope := &createScope{
		client:      fake.Client,
		log:         logger.Nop(),
		containerID: "cid-123",
		volumes:     []string{"vol-a", "vol-b"},
	}
	scope.reclaim()

	require.Equal(t, []string{"vol-a", "vol-b"}, removed,
		"a failed removal must not abort the remaining cleanups")
}

// TestCreateScope_Reclaim_NoContainer covers a pre-create failure: no container
// was created yet, so only the newly-created volumes are reclaimed.
func TestCreateScope_Reclaim_NoContainer(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())

	var removed []string
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, id string, _ moby.VolumeRemoveOptions) (moby.VolumeRemoveResult, error) {
		removed = append(removed, id)
		return moby.VolumeRemoveResult{}, nil
	}

	scope := &createScope{
		client:  fake.Client,
		log:     logger.Nop(),
		volumes: []string{"vol-a"},
	}
	scope.reclaim()

	fake.AssertNotCalled(t, "ContainerRemove")
	require.Equal(t, []string{"vol-a"}, removed)
}
