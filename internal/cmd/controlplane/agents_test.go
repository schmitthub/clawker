package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
)

// agentsHarness drives the agents verb against a mock AdminService
// gRPC client. The verb's only data path is f.AdminClient(ctx).ListAgents
// — CP is the SOLE writer of the registry now, so the host can no
// longer read sqlite directly.
type agentsHarness struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	opts      *AgentsOptions
}

func newAgentsHarness(t *testing.T, mock *adminv1mocks.AdminServiceClientMock) *agentsHarness {
	t.Helper()
	ios, _, _, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h := &agentsHarness{
		IOStreams: ios,
		TUI:       tuiInst,
	}
	h.opts = &AgentsOptions{
		IOStreams: ios,
		TUI:       tuiInst,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mock, nil
		},
		Format: &cmdutil.FormatFlags{},
	}
	return h
}

func TestAgentsRun_EmptyResponse(t *testing.T) {
	mock := &adminv1mocks.AdminServiceClientMock{
		ListAgentsFunc: func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
			return &adminv1.ListAgentsResult{}, nil
		},
	}
	h := newAgentsHarness(t, mock)
	ios, _, stdout, stderr := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	require.NoError(t, agentsRun(context.Background(), h.opts))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "No agents registered")
}

func TestAgentsRun_RendersTable(t *testing.T) {
	mock := &adminv1mocks.AdminServiceClientMock{
		ListAgentsFunc: func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
			return &adminv1.ListAgentsResult{
				Agents: []*adminv1.Agent{{
					AgentName:        "alpha",
					Project:          "myapp",
					ContainerId:      "0123456789abcdef0123456789abcdef",
					CertThumbprint:   "aaaaaaaaaaaa1111222233334444555566667777888899990000aaaabbbbcccc",
					RegisteredAtUnix: 1717000000,
					LastSeenUnix:     1717000000,
				}},
			}, nil
		},
	}
	h := newAgentsHarness(t, mock)
	ios, _, stdout, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	require.NoError(t, agentsRun(context.Background(), h.opts))
	out := stdout.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "myapp")
	assert.Contains(t, out, "0123456789ab") // 12-char short container id
	assert.Contains(t, out, "aaaaaaaaaaaa") // 12-char short thumbprint hex
}

func TestAgentsRun_JSONOutput(t *testing.T) {
	mock := &adminv1mocks.AdminServiceClientMock{
		ListAgentsFunc: func(_ context.Context, _ *adminv1.ListAgentsRequest, _ ...grpc.CallOption) (*adminv1.ListAgentsResult, error) {
			return &adminv1.ListAgentsResult{
				Agents: []*adminv1.Agent{{
					AgentName:        "x",
					Project:          "p",
					ContainerId:      "ctr",
					CertThumbprint:   "abcd",
					RegisteredAtUnix: 1,
					LastSeenUnix:     2,
				}},
			}, nil
		},
	}
	h := newAgentsHarness(t, mock)
	ios, _, stdout, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	jsonFmt, err := cmdutil.ParseFormat("json")
	require.NoError(t, err)
	h.opts.Format = &cmdutil.FormatFlags{Format: jsonFmt}

	require.NoError(t, agentsRun(context.Background(), h.opts))
	var rows []agentRow
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "x", rows[0].AgentName)
	assert.Equal(t, "p", rows[0].Project)
	assert.Equal(t, "ctr", rows[0].ContainerID)
}

func TestAgentsRun_PropagatesAdminClientError(t *testing.T) {
	h := newAgentsHarness(t, &adminv1mocks.AdminServiceClientMock{})
	h.opts.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return nil, errors.New("dial boom")
	}
	err := agentsRun(context.Background(), h.opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial boom")
}
