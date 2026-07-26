package status //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// newStatusCmd creates a status command wired to a Factory whose
// AdminServiceClient mock answers FirewallStatus with resp (or statusErr).
// Factory.Client is deliberately left nil: statusRun skips the CP-running
// probe when it has no Docker client, which keeps the RPC path under test
// without a Docker daemon.
//
//nolint:exhaustruct // mock wires only the RPC status drives; the Factory only the nouns it reads
func newStatusCmd(
	t *testing.T,
	resp *adminv1.FirewallStatusResult,
	statusErr error,
) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, stdout, _ := iostreams.Test()

	mock := &adminv1mocks.AdminServiceClientMock{
		FirewallStatusFunc: func(_ context.Context, _ *adminv1.FirewallStatusRequest, _ ...grpc.CallOption) (*adminv1.FirewallStatusResult, error) {
			if statusErr != nil {
				return nil, statusErr
			}
			return resp, nil
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mock, nil
		},
	}

	return f, stdout
}

// healthyStatus is a fully-up stack: both containers healthy, rules loaded,
// and every optional address populated.
//
//nolint:exhaustruct // fixture sets only the fields the status renderer reads
func healthyStatus() *adminv1.FirewallStatusResult {
	return &adminv1.FirewallStatusResult{
		Running:       true,
		EnvoyHealth:   true,
		CorednsHealth: true,
		RuleCount:     3,
		EnvoyIp:       "10.0.1.5",
		CorednsIp:     "10.0.1.6",
		NetworkId:     "net-abc123",
	}
}

// degradedStatus is a stack that reports stopped with a stale-healthy Envoy
// and no addresses — the optional lines must stay suppressed.
//
//nolint:exhaustruct // fixture sets only the fields the status renderer reads
func degradedStatus() *adminv1.FirewallStatusResult {
	return &adminv1.FirewallStatusResult{
		Running:       false,
		EnvoyHealth:   true,
		CorednsHealth: false,
		RuleCount:     0,
	}
}

// TestNewCmdStatus asserts the constructor parses the format flags onto
// StatusOptions before the run function sees them.
func TestNewCmdStatus(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantJSON     bool
		wantQuiet    bool
		wantTemplate bool
	}{
		{name: "no flags", args: []string{}, wantJSON: false, wantQuiet: false, wantTemplate: false},
		{name: "json", args: []string{"--json"}, wantJSON: true, wantQuiet: false, wantTemplate: false},
		{
			name:         "format json",
			args:         []string{"--format", "json"},
			wantJSON:     true,
			wantQuiet:    false,
			wantTemplate: false,
		},
		{name: "quiet", args: []string{"--quiet"}, wantJSON: false, wantQuiet: true, wantTemplate: false},
		{name: "quiet short", args: []string{"-q"}, wantJSON: false, wantQuiet: true, wantTemplate: false},
		{
			name:         "template",
			args:         []string{"--format", "{{.RuleCount}} rules active"},
			wantJSON:     false,
			wantQuiet:    false,
			wantTemplate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := newStatusCmd(t, healthyStatus(), nil)

			var gotOpts *StatusOptions
			cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			require.NotNil(t, gotOpts)
			assert.Equal(t, f.IOStreams, gotOpts.IOStreams)
			require.NotNil(t, gotOpts.Format)
			assert.Equal(t, tt.wantJSON, gotOpts.Format.IsJSON())
			assert.Equal(t, tt.wantQuiet, gotOpts.Format.Quiet)
			assert.Equal(t, tt.wantTemplate, gotOpts.Format.IsTemplate())
		})
	}
}

func TestStatusRun_Renders(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		resp   *adminv1.FirewallStatusResult
		verify func(t *testing.T, ios *iostreams.IOStreams, stdout string)
	}{
		{
			name: "table healthy",
			args: []string{},
			resp: healthyStatus(),
			verify: func(t *testing.T, ios *iostreams.IOStreams, stdout string) {
				t.Helper()
				cs := ios.ColorScheme()
				assert.Contains(t, stdout, "Firewall:  running")
				assert.Contains(t, stdout, "Envoy:     "+cs.SuccessIcon())
				assert.Contains(t, stdout, "CoreDNS:   "+cs.SuccessIcon())
				assert.Contains(t, stdout, "Rules:     3 active")
				assert.Contains(t, stdout, "Envoy IP:  10.0.1.5")
				assert.Contains(t, stdout, "DNS IP:    10.0.1.6")
				assert.Contains(t, stdout, "Network:   net-abc123")
			},
		},
		{
			name: "table degraded suppresses empty address lines",
			args: []string{},
			resp: degradedStatus(),
			verify: func(t *testing.T, ios *iostreams.IOStreams, stdout string) {
				t.Helper()
				cs := ios.ColorScheme()
				assert.Contains(t, stdout, "Firewall:  stopped")
				assert.Contains(t, stdout, "Envoy:     "+cs.SuccessIcon())
				assert.Contains(t, stdout, "CoreDNS:   "+cs.FailureIcon())
				assert.Contains(t, stdout, "Rules:     0 active")
				assert.NotContains(t, stdout, "Envoy IP:")
				assert.NotContains(t, stdout, "DNS IP:")
				assert.NotContains(t, stdout, "Network:")
			},
		},
		{
			name: "json",
			args: []string{"--json"},
			resp: healthyStatus(),
			verify: func(t *testing.T, _ *iostreams.IOStreams, stdout string) {
				t.Helper()
				var row statusRow
				require.NoError(t, json.Unmarshal([]byte(stdout), &row))
				assert.True(t, row.Running)
				assert.True(t, row.EnvoyHealth)
				assert.True(t, row.CoreDNSHealth)
				assert.Equal(t, 3, row.RuleCount)
				assert.Equal(t, "10.0.1.5", row.EnvoyIP)
				assert.Equal(t, "10.0.1.6", row.CoreDNSIP)
				assert.Equal(t, "net-abc123", row.NetworkID)
			},
		},
		{
			name: "template",
			args: []string{"--format", "{{.RuleCount}} rules active"},
			resp: healthyStatus(),
			verify: func(t *testing.T, _ *iostreams.IOStreams, stdout string) {
				t.Helper()
				assert.Equal(t, "3 rules active", strings.TrimSpace(stdout))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, stdout := newStatusCmd(t, tt.resp, nil)
			cmd := NewCmdStatus(f, nil)
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			tt.verify(t, f.IOStreams, stdout.String())
		})
	}
}

// TestStatusRun_StatusError asserts an RPC failure surfaces as an error
// naming the operation, and that nothing is rendered to stdout.
func TestStatusRun_StatusError(t *testing.T) {
	f, stdout := newStatusCmd(t, nil, errors.New("control plane unreachable"))
	cmd := NewCmdStatus(f, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting firewall status")
	assert.Empty(t, stdout.String())
}
