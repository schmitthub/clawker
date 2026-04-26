package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// agentsHarness wires an AdminServiceClientMock through f.AdminClient
// without dragging in the cpboot.Manager harness used by the other
// commands — the agents verb only talks to AdminService.
type agentsHarness struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	Stdout    *bytes.Buffer
	Stderr    *bytes.Buffer
	AdminMock *cpmocks.AdminServiceClientMock
	opts      *AgentsOptions
}

func newAgentsHarness(t *testing.T) *agentsHarness {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()
	tui := tui.NewTUI(ios)
	mock := &cpmocks.AdminServiceClientMock{}
	h := &agentsHarness{
		IOStreams: ios,
		TUI:       tui,
		Stdout:    stdout,
		Stderr:    stderr,
		AdminMock: mock,
	}
	h.opts = &AgentsOptions{
		IOStreams: ios,
		TUI:       tui,
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mock, nil
		},
		Format: &cmdutil.FormatFlags{},
	}
	return h
}

func TestAgentsRun_EmptyRegistry(t *testing.T) {
	h := newAgentsHarness(t)
	h.AdminMock.ListAgentsFunc = func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
		return &adminv1.ListAgentsResult{}, nil
	}

	require.NoError(t, agentsRun(context.Background(), h.opts))
	// Empty result hits the stderr "no agents" branch — stdout should
	// be untouched so script consumers reading stdout get an empty
	// payload rather than a status line.
	assert.Empty(t, h.Stdout.String())
	assert.Contains(t, h.Stderr.String(), "No agents registered")
}

func TestAgentsRun_RendersTable(t *testing.T) {
	h := newAgentsHarness(t)
	h.AdminMock.ListAgentsFunc = func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
		return &adminv1.ListAgentsResult{
			Agents: []*adminv1.Agent{
				{
					AgentName:        "alpha",
					Project:          "myapp",
					ContainerId:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					CertThumbprint:   "deadbeef0123456789abcdef0123456789abcdef0123456789abcdef0123beef",
					RegisteredAtUnix: 1717000000,
					LastSeenUnix:     1717000123,
				},
			},
		}, nil
	}

	require.NoError(t, agentsRun(context.Background(), h.opts))
	out := h.Stdout.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "myapp")        // project column populated
	assert.Contains(t, out, "0123456789ab") // 12-char short container id
	assert.Contains(t, out, "deadbeef0123") // 12-char short thumbprint
}

func TestAgentsRun_JSONOutput(t *testing.T) {
	h := newAgentsHarness(t)
	h.AdminMock.ListAgentsFunc = func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
		return &adminv1.ListAgentsResult{
			Agents: []*adminv1.Agent{
				{AgentName: "x", Project: "p", ContainerId: "ctr", CertThumbprint: "tp", RegisteredAtUnix: 1, LastSeenUnix: 2},
			},
		}, nil
	}

	// Force JSON via the public ParseFormat seam so the test doesn't
	// need a cobra command to drive flag binding.
	jsonFmt, err := cmdutil.ParseFormat("json")
	require.NoError(t, err)
	h.opts.Format = &cmdutil.FormatFlags{Format: jsonFmt}

	require.NoError(t, agentsRun(context.Background(), h.opts))
	var rows []agentRow
	require.NoError(t, json.Unmarshal(h.Stdout.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "x", rows[0].AgentName)
	assert.Equal(t, "p", rows[0].Project)
}

func TestAgentsRun_PropagatesAdminClientError(t *testing.T) {
	h := newAgentsHarness(t)
	h.opts.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return nil, errors.New("dial failed")
	}
	err := agentsRun(context.Background(), h.opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial failed")
}

func TestAgentsRun_PropagatesRPCError(t *testing.T) {
	h := newAgentsHarness(t)
	h.AdminMock.ListAgentsFunc = func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
		return nil, errors.New("rpc failed")
	}
	err := agentsRun(context.Background(), h.opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc failed")
}
