package add //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
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
)

// addClient is a mock AdminService client for the single RPC add drives.
// result/rpcErr drive the FirewallAddRules outcome; requests are read back via
// moq's recorded Calls accessors.
func addClient(result *adminv1.FirewallAddRulesResult, rpcErr error) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
			if rpcErr != nil {
				return nil, rpcErr
			}
			return result, nil
		},
	}
}

// statusResult wraps a single rule status in a response.
func statusResult(status adminv1.AddRuleStatus, stackRestarted bool) *adminv1.FirewallAddRulesResult {
	return &adminv1.FirewallAddRulesResult{
		Statuses:       []adminv1.AddRuleStatus{status},
		StackRestarted: stackRestarted,
	}
}

// added is the common one-rule ADDED, stack-reloaded response.
func added() *adminv1.FirewallAddRulesResult {
	return statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED, true)
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

func assertNoneContains(t *testing.T, got string, unwanted []string) {
	t.Helper()
	for _, w := range unwanted {
		assert.NotContains(t, got, w)
	}
}

// TestNewCmdAdd pins flag/arg parsing onto AddOptions and asserts the
// required-together wiring rejects a half-specified path rule before the run
// function is ever reached.
func TestNewCmdAdd(t *testing.T) {
	tests := []struct {
		name        string
		input       []string
		wantErr     bool
		wantDomain  string
		wantProto   string
		wantPort    string
		wantPath    string
		wantAction  string
		wantMethods []string
	}{
		{
			name:        "domain only defaults to https",
			input:       []string{"registry.npmjs.org"},
			wantErr:     false,
			wantDomain:  "registry.npmjs.org",
			wantProto:   "https",
			wantPort:    "",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
		{
			name:        "proto and port",
			input:       []string{"git.example.com", "--proto", "ssh", "--port", "22"},
			wantErr:     false,
			wantDomain:  "git.example.com",
			wantProto:   "ssh",
			wantPort:    "22",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
		{
			name:        "port range",
			input:       []string{"api.example.com", "--port", "9000-9100"},
			wantErr:     false,
			wantDomain:  "api.example.com",
			wantProto:   "https",
			wantPort:    "9000-9100",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
		{
			name:        "path and action",
			input:       []string{"api.example.com", "--path", "/v1", "--action", "deny"},
			wantErr:     false,
			wantDomain:  "api.example.com",
			wantProto:   "https",
			wantPort:    "",
			wantPath:    "/v1",
			wantAction:  "deny",
			wantMethods: nil,
		},
		{
			name:        "methods csv",
			input:       []string{"api.github.com", "--path", "/", "--action", "allow", "--methods", "GET,HEAD"},
			wantErr:     false,
			wantDomain:  "api.github.com",
			wantProto:   "https",
			wantPort:    "",
			wantPath:    "/",
			wantAction:  "allow",
			wantMethods: []string{"GET", "HEAD"},
		},
		{
			name:        "path without action",
			input:       []string{"api.example.com", "--path", "/v1"},
			wantErr:     true,
			wantDomain:  "",
			wantProto:   "",
			wantPort:    "",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
		{
			name:        "action without path",
			input:       []string{"api.example.com", "--action", "allow"},
			wantErr:     true,
			wantDomain:  "",
			wantProto:   "",
			wantPort:    "",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
		{
			name:        "missing domain arg",
			input:       nil,
			wantErr:     true,
			wantDomain:  "",
			wantProto:   "",
			wantPort:    "",
			wantPath:    "",
			wantAction:  "",
			wantMethods: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			//nolint:exhaustruct // factory wires only the fields the add command reads
			f := &cmdutil.Factory{
				IOStreams: ios,
				Logger: func() (*logger.Logger, error) {
					return logger.Nop(), nil
				},
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return addClient(added(), nil), nil
				},
			}

			var gotOpts *AddOptions
			cmd := NewCmdAdd(f, func(_ context.Context, opts *AddOptions) error {
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
				assert.Nil(t, gotOpts, "run function must not be reached on a flag violation")
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
			assert.Equal(t, tt.wantAction, gotOpts.Action)
			assert.Equal(t, tt.wantMethods, gotOpts.Methods)
		})
	}
}

// TestAddRun_RejectsInvalidInput covers the validation that fires before any
// RPC: a bad --action, --methods without a path, a path on an opaque proto
// (where it could never be enforced), and a malformed --port. Every case must
// surface a FlagError so Cobra prints usage, and must leave the store
// untouched.
func TestAddRun_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		proto   string
		port    string
		path    string
		action  string
		methods []string
		wantErr string
	}{
		{
			name:    "invalid action",
			domain:  "api.example.com",
			proto:   "https",
			port:    "",
			path:    "/v1",
			action:  "block",
			methods: nil,
			wantErr: `must be "allow" or "deny"`,
		},
		{
			name:    "methods without path",
			domain:  "api.example.com",
			proto:   "https",
			port:    "",
			path:    "",
			action:  "",
			methods: []string{"GET"},
			wantErr: "--methods requires --path and --action",
		},
		{
			name:    "path on opaque proto",
			domain:  "git.example.com",
			proto:   "ssh",
			port:    "",
			path:    "/x",
			action:  "allow",
			methods: nil,
			wantErr: "https/http/ws/wss",
		},
		{
			name:    "port above range",
			domain:  "api.example.com",
			proto:   "https",
			port:    "65536",
			path:    "",
			action:  "",
			methods: nil,
			wantErr: "--port",
		},
		{
			name:    "inverted port range",
			domain:  "api.example.com",
			proto:   "https",
			port:    "9100-9000",
			path:    "",
			action:  "",
			methods: nil,
			wantErr: "--port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := iostreams.Test()
			mock := addClient(nil, nil)
			opts := &AddOptions{
				IOStreams: ios,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return mock, nil
				},
				Domain:  tt.domain,
				Proto:   tt.proto,
				Port:    tt.port,
				Path:    tt.path,
				Action:  tt.action,
				Methods: tt.methods,
			}

			err := addRun(context.Background(), opts)
			requireRunErr(t, err, tt.wantErr, nil, true)
			assert.Empty(t, mock.FirewallAddRulesCalls(), "no rule may reach the control plane")
			assert.Empty(t, stdout.String())
		})
	}
}

// TestAddRun_BuildsRequest pins the outbound rule shape: a bare domain stays a
// plain allow, --path/--action ride as a PathRule while the rule-level action
// stays "allow" (whitelist model), --methods narrow that PathRule, and the
// legacy tls alias is rewritten to https before the rule is sent.
func TestAddRun_BuildsRequest(t *testing.T) {
	tests := []struct {
		name      string
		domain    string
		proto     string
		port      string
		path      string
		action    string
		methods   []string
		assertReq func(*testing.T, *adminv1.FirewallAddRulesRequest)
	}{
		{
			name:    "bare domain sends allow rule",
			domain:  "api.example.com",
			proto:   "https",
			port:    "",
			path:    "",
			action:  "",
			methods: nil,
			assertReq: func(t *testing.T, req *adminv1.FirewallAddRulesRequest) {
				t.Helper()
				require.Len(t, req.GetRules(), 1)
				rule := req.GetRules()[0]
				assert.Equal(t, "api.example.com", rule.GetDst())
				assert.Equal(t, "https", rule.GetProto())
				assert.Equal(t, "allow", rule.GetAction())
				assert.Empty(t, rule.GetPathRules())
			},
		},
		{
			name:    "port range forwarded",
			domain:  "api.example.com",
			proto:   "https",
			port:    "9000-9100",
			path:    "",
			action:  "",
			methods: nil,
			assertReq: func(t *testing.T, req *adminv1.FirewallAddRulesRequest) {
				t.Helper()
				require.Len(t, req.GetRules(), 1)
				assert.Equal(t, "9000-9100", req.GetRules()[0].GetPort())
			},
		},
		{
			name:    "path flag builds path scoped rule",
			domain:  "api.example.com",
			proto:   "https",
			port:    "",
			path:    "/v1",
			action:  "deny",
			methods: nil,
			assertReq: func(t *testing.T, req *adminv1.FirewallAddRulesRequest) {
				t.Helper()
				require.Len(t, req.GetRules(), 1)
				rule := req.GetRules()[0]
				assert.Equal(t, "allow", rule.GetAction(), "rule-level Action stays allow under whitelist model")
				require.Len(t, rule.GetPathRules(), 1)
				assert.Equal(t, "/v1", rule.GetPathRules()[0].GetPath())
				assert.Equal(t, "deny", rule.GetPathRules()[0].GetAction())
			},
		},
		{
			name:    "methods attached to path rule",
			domain:  "api.github.com",
			proto:   "https",
			port:    "",
			path:    "/",
			action:  "allow",
			methods: []string{"GET", "HEAD"},
			assertReq: func(t *testing.T, req *adminv1.FirewallAddRulesRequest) {
				t.Helper()
				require.Len(t, req.GetRules(), 1)
				require.Len(t, req.GetRules()[0].GetPathRules(), 1)
				assert.Equal(t, []string{"GET", "HEAD"}, req.GetRules()[0].GetPathRules()[0].GetMethods())
			},
		},
		{
			name:    "tls alias normalized to https",
			domain:  "api.example.com",
			proto:   "tls",
			port:    "",
			path:    "/v1",
			action:  "allow",
			methods: nil,
			assertReq: func(t *testing.T, req *adminv1.FirewallAddRulesRequest) {
				t.Helper()
				require.Len(t, req.GetRules(), 1)
				assert.Equal(t, "https", req.GetRules()[0].GetProto(), "tls alias must be rewritten to https")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			mock := addClient(added(), nil)
			opts := &AddOptions{
				IOStreams: ios,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					return mock, nil
				},
				Domain:  tt.domain,
				Proto:   tt.proto,
				Port:    tt.port,
				Path:    tt.path,
				Action:  tt.action,
				Methods: tt.methods,
			}

			require.NoError(t, addRun(context.Background(), opts))

			calls := mock.FirewallAddRulesCalls()
			require.Len(t, calls, 1)
			tt.assertReq(t, calls[0].In)
		})
	}
}

// TestAddRun_RendersStatus covers the per-status output contract — ADDED,
// MODIFIED and UNCHANGED each in their bare-domain and path-scoped wording —
// plus the stack-not-restarted note, the statuses-length wire guard, and the
// error paths (unknown status, RPC failure, dial failure).
func TestAddRun_RendersStatus(t *testing.T) {
	rpcErr := errors.New("cp unreachable")
	dialErr := errors.New("dial: boom")

	tests := []struct {
		name          string
		path          string
		action        string
		result        *adminv1.FirewallAddRulesResult
		rpcErr        error
		dialErr       error
		wantErr       string
		wantErrIs     error
		wantCalls     int
		wantStdout    []string
		wantNotStdout []string
		wantStderr    []string
	}{
		{
			name:          "added without path",
			path:          "",
			action:        "",
			result:        added(),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Added rule: api.example.com (https)"},
			wantNotStdout: nil,
			wantStderr:    nil,
		},
		{
			name:          "added with path",
			path:          "/v1",
			action:        "deny",
			result:        added(),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Added path rule /v1 (deny) on api.example.com"},
			wantNotStdout: nil,
			wantStderr:    nil,
		},
		{
			name:          "modified with path prints updated line",
			path:          "/v1",
			action:        "allow",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Updated path rule"},
			wantNotStdout: []string{"Added path rule", "already exists"},
			wantStderr:    nil,
		},
		{
			name:          "modified without path prints updated rule line",
			path:          "",
			action:        "",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Updated rule: api.example.com (https)"},
			wantNotStdout: []string{"Added rule"},
			wantStderr:    nil,
		},
		{
			name:          "unchanged without path prints info line",
			path:          "",
			action:        "",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Rule already exists", "no change"},
			wantNotStdout: []string{"Added rule"},
			wantStderr:    nil,
		},
		{
			name:          "unchanged with path prints info line",
			path:          "/v1",
			action:        "allow",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Path rule already exists", "/v1"},
			wantNotStdout: []string{"Added path rule"},
			wantStderr:    nil,
		},
		{
			name:          "stack not restarted prints note",
			path:          "",
			action:        "",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED, false),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    []string{"Added rule"},
			wantNotStdout: nil,
			wantStderr:    []string{"rule persisted", "will take effect on next"},
		},
		{
			name:          "statuses length mismatch errors",
			path:          "",
			action:        "",
			result:        &adminv1.FirewallAddRulesResult{Statuses: nil, StackRestarted: true},
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "0 statuses",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    nil,
			wantNotStdout: nil,
			wantStderr:    nil,
		},
		{
			name:          "unspecified status errors",
			path:          "",
			action:        "",
			result:        statusResult(adminv1.AddRuleStatus_ADD_RULE_STATUS_UNSPECIFIED, true),
			rpcErr:        nil,
			dialErr:       nil,
			wantErr:       "unknown status",
			wantErrIs:     nil,
			wantCalls:     1,
			wantStdout:    nil,
			wantNotStdout: nil,
			wantStderr:    nil,
		},
		{
			name:          "rpc error wrapped",
			path:          "",
			action:        "",
			result:        nil,
			rpcErr:        rpcErr,
			dialErr:       nil,
			wantErr:       "adding firewall rule",
			wantErrIs:     rpcErr,
			wantCalls:     1,
			wantStdout:    nil,
			wantNotStdout: nil,
			wantStderr:    nil,
		},
		{
			name:          "dial error short circuits",
			path:          "",
			action:        "",
			result:        nil,
			rpcErr:        nil,
			dialErr:       dialErr,
			wantErr:       "connecting to control plane",
			wantErrIs:     dialErr,
			wantCalls:     0,
			wantStdout:    nil,
			wantNotStdout: nil,
			wantStderr:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, stderr := iostreams.Test()
			mock := addClient(tt.result, tt.rpcErr)
			opts := &AddOptions{
				IOStreams: ios,
				AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
					if tt.dialErr != nil {
						return nil, tt.dialErr
					}
					return mock, nil
				},
				Domain:  "api.example.com",
				Proto:   "https",
				Port:    "",
				Path:    tt.path,
				Action:  tt.action,
				Methods: nil,
			}

			err := addRun(context.Background(), opts)
			requireRunErr(t, err, tt.wantErr, tt.wantErrIs, false)

			assert.Len(t, mock.FirewallAddRulesCalls(), tt.wantCalls)
			assertAllContains(t, stdout.String(), tt.wantStdout)
			assertNoneContains(t, stdout.String(), tt.wantNotStdout)
			assertAllContains(t, stderr.String(), tt.wantStderr)
		})
	}
}
