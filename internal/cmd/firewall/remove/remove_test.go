package remove //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// removeRuleClient is a mock AdminService client for the RPC remove drives.
// result/rpcErr drive the FirewallRemoveRule outcome; requests are read back
// via moq's recorded Calls accessors.
func removeRuleClient(result *adminv1.FirewallRemoveRuleResult, rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallRemoveRuleFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return result, nil
		},
	}
}

// listRulesClient is a mock AdminService client for the completion path.
func listRulesClient(rules []*adminv1.EgressRule, listErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallListRulesFunc: func(_ context.Context, _ *adminv1.FirewallListRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallListRulesResult, error) {
			if listErr != nil {
				return nil, listErr
			}
			return &adminv1.FirewallListRulesResult{Rules: rules}, nil
		},
	}
}

// statusResult wraps a removal status in a response.
func statusResult(status adminv1.RemoveRuleStatus, stackRestarted bool) *adminv1.FirewallRemoveRuleResult {
	return &adminv1.FirewallRemoveRuleResult{
		StackRestarted: stackRestarted,
		Status:         status,
	}
}

// testFactory builds a Factory wired with the supplied admin-client closure.
func testFactory(adminFn func(context.Context) (adminv1.AdminServiceClient, error)) *cmdutil.Factory {
	ios, _, _, _ := iostreams.Test() //nolint:dogsled // iostreams.Test returns three buffers this helper does not assert on
	//nolint:exhaustruct // factory wires only the fields the remove command reads
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
		AdminClient: adminFn,
	}
}

// requireRunErr asserts the error contract of a run-function call.
func requireRunErr(t *testing.T, err error, want string, wantIs error, wantFlagError bool) {
	t.Helper()
	if want == "" {
		require.NoError(t, err)
		return
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), want)
	if wantIs != nil {
		require.ErrorIs(t, err, wantIs)
	}
	if wantFlagError {
		var fe *cmdutil.FlagError
		require.ErrorAs(t, err, &fe, "must be a FlagError so Cobra displays usage")
	}
}

func assertAllContains(t *testing.T, got string, want []string) {
	t.Helper()
	for _, w := range want {
		assert.Contains(t, got, w)
	}
}

// TestNewCmdRemove pins flag/arg parsing onto RemoveOptions.
func TestNewCmdRemove(t *testing.T) {
	tests := []struct {
		name       string
		input      []string
		wantErr    bool
		wantDomain string
		wantProto  string
		wantPort   string
		wantPath   string
	}{
		{
			name:       "domain only defaults to https",
			input:      []string{"registry.npmjs.org"},
			wantErr:    false,
			wantDomain: "registry.npmjs.org",
			wantProto:  "https",
			wantPort:   "",
			wantPath:   "",
		},
		{
			name:       "proto and port",
			input:      []string{"git.example.com", "--proto", "ssh", "--port", "22"},
			wantErr:    false,
			wantDomain: "git.example.com",
			wantProto:  "ssh",
			wantPort:   "22",
			wantPath:   "",
		},
		{
			name:       "path scoped removal",
			input:      []string{"api.example.com", "--path", "/v1"},
			wantErr:    false,
			wantDomain: "api.example.com",
			wantProto:  "https",
			wantPort:   "",
			wantPath:   "/v1",
		},
		{
			name:       "missing domain arg",
			input:      nil,
			wantErr:    true,
			wantDomain: "",
			wantProto:  "",
			wantPort:   "",
			wantPath:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testFactory(func(_ context.Context) (adminv1.AdminServiceClient, error) {
				removed := statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, true)
				return removeRuleClient(removed, nil), nil
			})

			var gotOpts *RemoveOptions
			cmd := NewCmdRemove(f, func(_ context.Context, opts *RemoveOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.input)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err := cmd.Execute()
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, gotOpts, "run function must not be reached on an arg violation")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.NotNil(t, gotOpts.IOStreams)
			assert.NotNil(t, gotOpts.AdminClient)
			assert.Equal(t, tt.wantDomain, gotOpts.Domain)
			assert.Equal(t, tt.wantProto, gotOpts.Proto)
			assert.Equal(t, tt.wantPort, gotOpts.Port)
			assert.Equal(t, tt.wantPath, gotOpts.Path)
		})
	}
}

// TestRemoveRun covers the removal contract: the outbound tuple forwarded on
// the wire (including All staying false — the wipe belongs to prune), the
// per-status rendering (REMOVED / PATH_REMOVED), and the non-zero-exit
// NOT_FOUND error that keeps a typo from reading as success.
func TestRemoveRun(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	dialErr := errors.New("dial: boom")

	tests := []struct {
		name          string
		domain        string
		proto         string
		port          string
		path          string
		result        *adminv1.FirewallRemoveRuleResult
		rpcErr        error
		dialErr       error
		wantErr       string
		wantFlagError bool
		wantCalls     int
		assertReq     func(*testing.T, *adminv1.FirewallRemoveRuleRequest)
		wantStdout    []string
		wantStderr    []string
	}{
		{
			name:          "removes whole rule",
			domain:        "example.com",
			proto:         "tls",
			port:          "443",
			path:          "",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantFlagError: false,
			wantCalls:     1,
			assertReq: func(t *testing.T, req *adminv1.FirewallRemoveRuleRequest) {
				t.Helper()
				assert.Equal(t, "example.com", req.GetDst())
				assert.Equal(t, "tls", req.GetProto())
				assert.Equal(t, "443", req.GetPort())
				assert.Empty(t, req.GetPath())
				assert.False(t, req.GetAll(), "single-rule removal must never set the wipe-everything flag")
			},
			wantStdout: []string{"Removed rule: example.com"},
			wantStderr: nil,
		},
		{
			name:          "no path leaves request path empty",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantFlagError: false,
			wantCalls:     1,
			assertReq: func(t *testing.T, req *adminv1.FirewallRemoveRuleRequest) {
				t.Helper()
				assert.Empty(t, req.GetPath())
			},
			wantStdout: nil,
			wantStderr: nil,
		},
		{
			name:          "path scoped removal",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "/v1",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantFlagError: false,
			wantCalls:     1,
			assertReq: func(t *testing.T, req *adminv1.FirewallRemoveRuleRequest) {
				t.Helper()
				assert.Equal(t, "api.example.com", req.GetDst())
				assert.Equal(t, "/v1", req.GetPath())
			},
			wantStdout: []string{"Removed path rule /v1 on api.example.com"},
			wantStderr: nil,
		},
		{
			name:          "stack not restarted prints note",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED, false),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantFlagError: false,
			wantCalls:     1,
			assertReq:     nil,
			wantStdout:    []string{"Removed rule"},
			wantStderr:    []string{"rule removed", "will take effect on next"},
		},
		{
			name:          "not found exits non-zero",
			domain:        "exmaple.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "rule not found: exmaple.com",
			wantFlagError: false,
			wantCalls:     1,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
		{
			name:          "not found names the missing path",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "/unknown",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       `rule not found: api.example.com:https: path "/unknown"`,
			wantFlagError: false,
			wantCalls:     1,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
		{
			name:          "unspecified status errors",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        statusResult(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_UNSPECIFIED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "unknown status",
			wantFlagError: false,
			wantCalls:     1,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
		{
			name:          "invalid port rejected",
			domain:        "api.example.com",
			proto:         "https",
			port:          "65536",
			path:          "",
			result:        nil,
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "--port",
			wantFlagError: true,
			wantCalls:     0,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
		{
			name:          "rpc error wrapped",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        nil,
			rpcErr:        rpcErr,
			dialErr:       nil,
			wantErr:       "removing firewall rule",
			wantFlagError: false,
			wantCalls:     1,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
		{
			name:          "dial error short circuits",
			domain:        "api.example.com",
			proto:         "https",
			port:          "",
			path:          "",
			result:        nil,
			rpcErr:        nil,
			dialErr:       dialErr,
			wantErr:       "connecting to control plane",
			wantFlagError: false,
			wantCalls:     0,
			assertReq:     nil,
			wantStdout:    nil,
			wantStderr:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, stderr := iostreams.Test()
			mock := removeRuleClient(tt.result, tt.rpcErr)
			opts := &RemoveOptions{
				IOStreams: ios,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return mock, nil
				},
				Domain: tt.domain,
				Proto:  tt.proto,
				Port:   tt.port,
				Path:   tt.path,
			}

			wantIs := tt.rpcErr
			if tt.dialErr != nil {
				wantIs = tt.dialErr
			}

			err := removeRun(context.Background(), opts)
			requireRunErr(t, err, tt.wantErr, wantIs, tt.wantFlagError)

			calls := mock.FirewallRemoveRuleCalls()
			require.Len(t, calls, tt.wantCalls)
			if tt.assertReq != nil {
				tt.assertReq(t, calls[0].In)
			}

			assertAllContains(t, stdout.String(), tt.wantStdout)
			assertAllContains(t, stderr.String(), tt.wantStderr)
		})
	}
}

// TestRemoveCompletion covers the ValidArgsFunction: domains are deduplicated
// and sorted, and every failure mode degrades to no suggestions rather than
// surfacing an error into the user's shell.
func TestRemoveCompletion(t *testing.T) {
	tests := []struct {
		name    string
		rules   []*adminv1.EgressRule
		listErr error
		dialErr error
		args    []string
		want    []string
	}{
		{
			name: "returns sorted domains",
			rules: []*adminv1.EgressRule{
				{Dst: "zebra.example.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
				{Dst: "alpha.example.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
				{Dst: "middle.example.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
			},
			listErr: nil,
			dialErr: nil,
			args:    nil,
			want:    []string{"alpha.example.com", "middle.example.com", "zebra.example.com"},
		},
		{
			name: "deduplicates domains",
			rules: []*adminv1.EgressRule{
				{Dst: "example.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
				{Dst: "example.com", Proto: "ssh", Port: "22", Action: "", PathRules: nil, PathDefault: ""},
				{Dst: "other.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
			},
			listErr: nil,
			dialErr: nil,
			args:    nil,
			want:    []string{"example.com", "other.com"},
		},
		{
			name: "already has arg",
			rules: []*adminv1.EgressRule{
				{Dst: "example.com", Proto: "https", Port: "", Action: "", PathRules: nil, PathDefault: ""},
			},
			listErr: nil,
			dialErr: nil,
			args:    []string{"already-set"},
			want:    nil,
		},
		{
			name:    "list error",
			rules:   nil,
			listErr: errors.New("corrupt store"),
			dialErr: nil,
			args:    nil,
			want:    nil,
		},
		{
			name:    "client init error",
			rules:   nil,
			listErr: nil,
			dialErr: errors.New("CP unreachable"),
			args:    nil,
			want:    nil,
		},
		{
			name:    "empty rules",
			rules:   nil,
			listErr: nil,
			dialErr: nil,
			args:    nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testFactory(func(_ context.Context) (adminv1.AdminServiceClient, error) {
				if tt.dialErr != nil {
					return nil, tt.dialErr
				}
				return listRulesClient(tt.rules, tt.listErr), nil
			})

			cmd := NewCmdRemove(f, nil)
			cmd.SetContext(context.Background())

			completions, directive := cmd.ValidArgsFunction(cmd, tt.args, "")

			require.Len(t, completions, len(tt.want))
			for i, want := range tt.want {
				assert.Equal(t, want, completions[i])
			}
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
		})
	}
}
