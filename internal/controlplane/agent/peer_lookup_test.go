package agent

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakePeerLookupAPI is a hand-rolled peerLookupAPI for MobyPeerLookup
// tests. Closures let each test scope shape the daemon responses.
type fakePeerLookupAPI struct {
	listFn    func(ctx context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error)
	inspectFn func(ctx context.Context, id string, opts mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
}

func (f *fakePeerLookupAPI) ContainerList(ctx context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
	return f.listFn(ctx, opts)
}

func (f *fakePeerLookupAPI) ContainerInspect(ctx context.Context, id string, opts mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return f.inspectFn(ctx, id, opts)
}

type inspectFixture struct {
	containerID string
	project     string
	agentName   string
	ip          string
	networksNil bool // true → NetworkSettings == nil entirely
	configNil   bool // true → Config == nil (and therefore Labels nil)
	omitProject bool // true → drop dev.clawker.project label (empty-project agent)
	omitAgent   bool // true → drop dev.clawker.agent label (malformed)
}

func makeInspect(f inspectFixture) mobyclient.ContainerInspectResult {
	resp := mobycontainer.InspectResponse{ID: f.containerID}
	if !f.configNil {
		labels := map[string]string{}
		if !f.omitProject {
			labels[consts.LabelProject] = f.project
		}
		if !f.omitAgent {
			labels[consts.LabelAgent] = f.agentName
		}
		resp.Config = &mobycontainer.Config{Labels: labels}
	}
	if !f.networksNil {
		networks := map[string]*mobynetwork.EndpointSettings{}
		if f.ip != "" {
			networks[consts.Network] = &mobynetwork.EndpointSettings{
				IPAddress: netip.MustParseAddr(f.ip),
			}
		}
		resp.NetworkSettings = &mobycontainer.NetworkSettings{Networks: networks}
	}
	return mobyclient.ContainerInspectResult{Container: resp}
}

func newTestLookup(api peerLookupAPI) *MobyPeerLookup {
	return &MobyPeerLookup{cli: api, log: logger.Nop()}
}

func mustProject(t *testing.T, s string) auth.ProjectSlug {
	t.Helper()
	p, err := auth.NewProjectSlug(s)
	require.NoError(t, err)
	return p
}

func mustAgent(t *testing.T, s string) auth.AgentName {
	t.Helper()
	a, err := auth.NewAgentName(s)
	require.NoError(t, err)
	return a
}

func TestContainerByPeerIP_MatchFirst(t *testing.T) {
	want := inspectFixture{containerID: "abc", project: "proj", agentName: "agent-a", ip: "10.0.0.5"}

	var sawFilter mobyclient.Filters
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			sawFilter = opts.Filters
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: want.containerID}}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			require.Equal(t, want.containerID, id)
			return makeInspect(want), nil
		},
	}
	got, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(want.ip))
	require.NoError(t, err)
	assert.Equal(t, ResolvedContainer{
		ContainerID: want.containerID,
		Project:     mustProject(t, want.project),
		AgentName:   mustAgent(t, want.agentName),
	}, got)

	// Filter assertion: the resolver MUST scope the ContainerList to
	// purpose=agent — without that, a non-agent container sharing an
	// IP could be returned as a "matched agent". Trust-anchor critical.
	expected := consts.LabelPurpose + "=" + consts.PurposeAgent
	require.NotNil(t, sawFilter, "ContainerList must be invoked with filters set")
	require.True(t, sawFilter["label"][expected], "label filter must include %q", expected)
}

func TestContainerByPeerIP_NoMatch(t *testing.T) {
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: "x"}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(inspectFixture{containerID: "x", project: "p", agentName: "a", ip: "10.0.0.99"}), nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	assert.ErrorIs(t, err, ErrNoContainerForPeerIP)
}

func TestContainerByPeerIP_MultipleCandidates_PicksMatch(t *testing.T) {
	wrong := inspectFixture{containerID: "wrong", project: "p", agentName: "a1", ip: "10.0.0.99"}
	right := inspectFixture{containerID: "right", project: "p", agentName: "a2", ip: "10.0.0.5"}
	other := inspectFixture{containerID: "other", project: "p", agentName: "a3", ip: "10.0.0.7"}
	byID := map[string]inspectFixture{wrong.containerID: wrong, right.containerID: right, other.containerID: other}

	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
				{ID: wrong.containerID}, {ID: right.containerID}, {ID: other.containerID},
			}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(byID[id]), nil
		},
	}
	got, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(right.ip))
	require.NoError(t, err)
	assert.Equal(t, right.containerID, got.ContainerID)
	assert.Equal(t, mustAgent(t, right.agentName), got.AgentName)
}

func TestContainerByPeerIP_SkipsContainerWithoutClawkerNet(t *testing.T) {
	noEndpoint := inspectFixture{containerID: "no-net", project: "p", agentName: "a", ip: ""}
	match := inspectFixture{containerID: "match", project: "p", agentName: "b", ip: "10.0.0.5"}
	byID := map[string]inspectFixture{noEndpoint.containerID: noEndpoint, match.containerID: match}

	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
				{ID: noEndpoint.containerID}, {ID: match.containerID},
			}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(byID[id]), nil
		},
	}
	got, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(match.ip))
	require.NoError(t, err)
	assert.Equal(t, match.containerID, got.ContainerID)
}

func TestContainerByPeerIP_NilNetworkSettings_Skipped(t *testing.T) {
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: "nilnet"}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(inspectFixture{containerID: "nilnet", project: "p", agentName: "a", networksNil: true}), nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	assert.ErrorIs(t, err, ErrNoContainerForPeerIP)
}

func TestContainerByPeerIP_NilConfig_InvalidLabels(t *testing.T) {
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: "no-cfg"}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(inspectFixture{containerID: "no-cfg", configNil: true, ip: "10.0.0.5"}), nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	assert.ErrorIs(t, err, ErrInvalidAgentLabel)
}

func TestContainerByPeerIP_MissingAgentLabel_InvalidLabels(t *testing.T) {
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: "no-agent"}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(inspectFixture{containerID: "no-agent", project: "p", omitAgent: true, ip: "10.0.0.5"}), nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	assert.ErrorIs(t, err, ErrInvalidAgentLabel)
}

func TestContainerByPeerIP_EmptyProjectLabel_Allowed(t *testing.T) {
	// Empty project is legitimate — clawker supports 2-segment agent
	// names (clawker.agent) where the project label is intentionally
	// omitted. auth.NewProjectSlug("") returns a zero-value slug, no
	// error. The resolver MUST accept it.
	want := inspectFixture{containerID: "noproj", agentName: "loner", omitProject: true, ip: "10.0.0.5"}
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: want.containerID}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return makeInspect(want), nil
		},
	}
	got, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(want.ip))
	require.NoError(t, err)
	assert.Equal(t, want.containerID, got.ContainerID)
	assert.Equal(t, mustAgent(t, want.agentName), got.AgentName)
	assert.True(t, got.Project == auth.ProjectSlug{}, "empty project label must produce zero-value ProjectSlug")
}

func TestContainerByPeerIP_ListError_Wrapped(t *testing.T) {
	want := errors.New("daemon unreachable")
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{}, want
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			t.Fatalf("inspect should not be reached when list fails")
			return mobyclient.ContainerInspectResult{}, nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

func TestContainerByPeerIP_InspectError_ContinuesAndAggregates(t *testing.T) {
	// One inspect fails; no other candidate matches. Expect the
	// daemon error to propagate so callers can distinguish "no agent"
	// from "we couldn't tell".
	want := errors.New("inspect blew up")
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{{ID: "boom"}}}, nil
		},
		inspectFn: func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			return mobyclient.ContainerInspectResult{}, want
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
	assert.False(t, errors.Is(err, ErrNoContainerForPeerIP),
		"aggregate inspect error must not masquerade as clean no-match")
}

func TestContainerByPeerIP_InspectError_StillFindsMatchAfter(t *testing.T) {
	// First candidate inspect fails; second matches. Iteration must
	// continue past the failure rather than abort.
	match := inspectFixture{containerID: "match", project: "p", agentName: "a", ip: "10.0.0.5"}
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
				{ID: "boom"}, {ID: match.containerID},
			}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			if id == "boom" {
				return mobyclient.ContainerInspectResult{}, errors.New("transient")
			}
			return makeInspect(match), nil
		},
	}
	got, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(match.ip))
	require.NoError(t, err)
	assert.Equal(t, match.containerID, got.ContainerID)
}

// TestContainerByPeerIP_AmbiguousMatch pins the trust-anchor
// fail-closed contract: when two purpose=agent containers advertise
// endpoints on the same clawker-network IP (transient stale endpoints
// during a restart cycle), the resolver returns ErrAmbiguousPeerIP
// rather than picking the first match. Grounding the trust anchor
// on first-match-wins would create a race window where an attacker
// could plant a stale endpoint and inherit a victim's identity.
func TestContainerByPeerIP_AmbiguousMatch(t *testing.T) {
	wantIP := "10.0.0.5"
	first := inspectFixture{containerID: "first", project: "p", agentName: "a", ip: wantIP}
	second := inspectFixture{containerID: "second", project: "p", agentName: "b", ip: wantIP}
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
				{ID: first.containerID}, {ID: second.containerID},
			}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			switch id {
			case first.containerID:
				return makeInspect(first), nil
			case second.containerID:
				return makeInspect(second), nil
			}
			t.Fatalf("unexpected inspect for %q", id)
			return mobyclient.ContainerInspectResult{}, nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr(wantIP))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAmbiguousPeerIP)
}

// TestContainerByPeerIP_InspectError_WithOtherCleanNoMatch pins the
// classification fix: if a single inspect failed but at least one
// other candidate inspected cleanly and didn't match, the absence is
// authoritative. Returning ErrNoContainerForPeerIP rather than the
// wrapped daemon error keeps the peer_lookup_no_match audit signal
// useful — operators relying on it to spot "agents connecting from
// unexpected IPs" would miss the case otherwise.
func TestContainerByPeerIP_InspectError_WithOtherCleanNoMatch(t *testing.T) {
	clean := inspectFixture{containerID: "clean", project: "p", agentName: "a", ip: "10.0.0.9"}
	api := &fakePeerLookupAPI{
		listFn: func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
			return mobyclient.ContainerListResult{Items: []mobycontainer.Summary{
				{ID: "boom"}, {ID: clean.containerID},
			}}, nil
		},
		inspectFn: func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
			if id == "boom" {
				return mobyclient.ContainerInspectResult{}, errors.New("transient daemon hiccup")
			}
			return makeInspect(clean), nil
		},
	}
	_, err := newTestLookup(api).LookupByIP(context.Background(), netip.MustParseAddr("10.0.0.5"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoContainerForPeerIP,
		"one clean inspect with no match outweighs a transient inspect failure")
}

// Compile-time guard: mobyclient.APIClient must satisfy peerLookupAPI.
// Catches a moby signature drift before it becomes a runtime wiring
// failure in NewMobyPeerLookup.
var _ peerLookupAPI = (mobyclient.APIClient)(nil)
