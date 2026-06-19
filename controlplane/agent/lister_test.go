package agent

import (
	"context"
	"errors"
	"testing"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// TestContainerLister_List_NonOverridableFilter is the load-bearing
// invariant test: every List call MUST narrow to BOTH the managed label
// AND purpose=agent, regardless of opts, so the scope can never widen
// past purpose=agent. It also confirms ListOpts.All is plumbed straight
// through to the daemon call and that the returned IDs are the container
// summaries' IDs in order.
func TestContainerLister_List_NonOverridableFilter(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	wantManagedLabel := cfg.LabelManaged() + "=" + cfg.ManagedLabelValue()
	wantPurposeLabel := cfg.LabelPurpose() + "=" + cfg.PurposeAgent()

	for _, tc := range []struct {
		name string
		opts ListOpts
	}{
		{name: "running only", opts: ListOpts{}},
		{name: "all including stopped", opts: ListOpts{All: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured mobyclient.ContainerListOptions
			fake := &whailtest.FakeAPIClient{
				ContainerListFn: func(_ context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
					captured = opts
					return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
						{ID: "agent-1"},
						{ID: "agent-2"},
					}}, nil
				},
			}

			lister := NewContainerLister(fake, cfg)
			ids, err := lister.List(context.Background(), tc.opts)
			require.NoError(t, err)
			assert.Equal(t, []string{"agent-1", "agent-2"}, ids)

			// All is plumbed straight through.
			assert.Equal(t, tc.opts.All, captured.All)

			// Both labels are always present — non-overridable.
			labelTerms := captured.Filters["label"]
			require.NotNil(t, labelTerms, "label filter term must be set")
			assert.True(t, labelTerms[wantManagedLabel], "managed label must be in filter")
			assert.True(t, labelTerms[wantPurposeLabel], "purpose=agent label must be in filter")
			assert.Len(t, labelTerms, 2, "only the two non-overridable labels may be set")
		})
	}
}

// TestContainerLister_List_PropagatesDaemonError confirms a daemon list
// failure surfaces to the caller (no swallow) and yields a nil ID slice.
func TestContainerLister_List_PropagatesDaemonError(t *testing.T) {
	wantErr := errors.New("daemon down")
	fake := &whailtest.FakeAPIClient{
		ContainerListFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{}, wantErr
		},
	}

	lister := NewContainerLister(fake, configmocks.NewBlankConfig())
	ids, err := lister.List(context.Background(), ListOpts{})
	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, ids)
}
