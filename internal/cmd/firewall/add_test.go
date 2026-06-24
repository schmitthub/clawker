package firewall

import (
	"bytes"
	"context"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// testFactoryWithStreams returns a Factory + the captured stdout/stderr
// buffers so add-command tests can assert on emitted text.
func testFactoryWithStreams(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
	return f, out, errOut
}

// TestAddCmd_PathAlone_FailsValidation asserts MarkFlagsRequiredTogether
// catches the operator passing --path without --action. The behavioral
// guarantee is that the RPC must not fire; the test asserts that, not
// Cobra's error wording (which can shift across versions).
func TestAddCmd_PathAlone_FailsValidation(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.False(t, called, "RPC must not fire when --path is set without --action")
}

// TestAddCmd_ActionAlone_FailsValidation is the symmetric case.
func TestAddCmd_ActionAlone_FailsValidation(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--action", "allow"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.False(t, called, "RPC must not fire when --action is set without --path")
}

// TestAddCmd_InvalidAction_Rejected verifies --action is constrained to
// allow|deny before any RPC fires.
func TestAddCmd_InvalidAction_Rejected(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1", "--action", "block"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must be "allow" or "deny"`)
	assert.False(t, called, "RPC must not fire on bad --action value")
}

// TestAddCmd_PathFlag_BuildsPathScopedRule asserts the outbound request
// shape when --path/--action are set: one PathRule on the rule, rule-level
// Action remains "allow" (whitelist semantics).
func TestAddCmd_PathFlag_BuildsPathScopedRule(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	var got *adminv1.FirewallAddRulesRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				got = req
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1", "--action", "deny"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.GetRules(), 1)
	rule := got.GetRules()[0]
	assert.Equal(t, "api.example.com", rule.GetDst())
	assert.Equal(t, "allow", rule.GetAction(), "rule-level Action stays allow under whitelist model")
	require.Len(t, rule.GetPathRules(), 1)
	assert.Equal(t, "/v1", rule.GetPathRules()[0].GetPath())
	assert.Equal(t, "deny", rule.GetPathRules()[0].GetAction())
}

// TestAddCmd_NoPathFlag_BackwardCompatible asserts the no-flag path still
// produces a bare-domain Action=allow rule with no PathRules.
func TestAddCmd_NoPathFlag_BackwardCompatible(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	var got *adminv1.FirewallAddRulesRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				got = req
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.GetRules(), 1)
	rule := got.GetRules()[0]
	assert.Equal(t, "allow", rule.GetAction())
	assert.Empty(t, rule.GetPathRules())
}

// TestAddCmd_AlreadyExists_NoPath_PrintsInfoLine asserts that when the
// server returns Statuses[0] == ADD_RULE_STATUS_UNCHANGED the CLI surfaces
// an "already exists" line instead of the misleading "Added rule" success
// line.
func TestAddCmd_AlreadyExists_NoPath_PrintsInfoLine(t *testing.T) {
	f, out, _ := testFactoryWithStreams(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com"})
	err := cmd.Execute()
	require.NoError(t, err)
	stdout := out.String()
	assert.Contains(t, stdout, "already exists")
	assert.NotContains(t, stdout, "Added rule")
}

// TestAddCmd_Modified_PrintsUpdatedLine asserts the MODIFIED status path
// (merge mutated an existing rule) renders "Updated rule" — the bool
// added_count surface used to collapse this into a misleading "Added".
func TestAddCmd_Modified_PrintsUpdatedLine(t *testing.T) {
	f, out, _ := testFactoryWithStreams(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_MODIFIED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1", "--action", "allow"})
	err := cmd.Execute()
	require.NoError(t, err)
	stdout := out.String()
	assert.Contains(t, stdout, "Updated path rule")
	assert.NotContains(t, stdout, "Added path rule")
	assert.NotContains(t, stdout, "already exists")
}

// TestAddCmd_StatusesLengthMismatch_Errors guards the wire contract: the
// CLI sends one rule, so the response must carry exactly one status.
// A server bug returning [] or [a, b] surfaces as an error, never silent
// success.
func TestAddCmd_StatusesLengthMismatch_Errors(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				return &adminv1.FirewallAddRulesResult{}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0 statuses")
}

// TestAddCmd_AlreadyExists_WithPath_PrintsInfoLine is the path-scoped
// variant of the above.
func TestAddCmd_AlreadyExists_WithPath_PrintsInfoLine(t *testing.T) {
	f, out, _ := testFactoryWithStreams(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1", "--action", "allow"})
	err := cmd.Execute()
	require.NoError(t, err)
	stdout := out.String()
	assert.Contains(t, stdout, "Path rule already exists")
	assert.Contains(t, stdout, "/v1")
	assert.NotContains(t, stdout, "Added path rule")
}

// TestAddCmd_Methods_AttachedToPathRule asserts --methods rides onto the
// path rule in the outbound request.
func TestAddCmd_Methods_AttachedToPathRule(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	var got *adminv1.FirewallAddRulesRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				got = req
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.github.com", "--path", "/", "--action", "allow", "--methods", "GET,HEAD"})
	require.NoError(t, cmd.Execute())
	require.NotNil(t, got)
	require.Len(t, got.GetRules(), 1)
	require.Len(t, got.GetRules()[0].GetPathRules(), 1)
	assert.Equal(t, []string{"GET", "HEAD"}, got.GetRules()[0].GetPathRules()[0].GetMethods())
}

// TestAddCmd_Methods_RequirePath rejects --methods without --path/--action
// before any RPC fires.
func TestAddCmd_Methods_RequirePath(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--methods", "GET"})
	require.Error(t, cmd.Execute())
	assert.False(t, called, "RPC must not fire when --methods is set without --path")
}

// TestAddCmd_PathOnOpaqueProto_Rejected gates --path/--methods to HTTP-family
// protos: an ssh rule with a path can never enforce the path, so it is rejected
// at input validation rather than silently producing an ignored rule.
func TestAddCmd_PathOnOpaqueProto_Rejected(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"git.example.com", "--proto", "ssh", "--path", "/x", "--action", "allow"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https/http/ws/wss")
	assert.False(t, called, "RPC must not fire for a path rule on an opaque proto")
}

// TestAddCmd_TLSAlias_NormalizedToHTTPS confirms the legacy `--proto tls` alias
// is rewritten to https in flight, so a path rule on it is accepted (not
// rejected by the HTTP-family gate) and the stored rule carries https.
func TestAddCmd_TLSAlias_NormalizedToHTTPS(t *testing.T) {
	f, _, _ := testFactoryWithStreams(t)
	var got *adminv1.FirewallAddRulesRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				got = req
				return &adminv1.FirewallAddRulesResult{Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED}}, nil
			},
		}, nil
	}
	cmd := NewCmdAdd(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--proto", "tls", "--path", "/v1", "--action", "allow"})
	require.NoError(t, cmd.Execute())
	require.NotNil(t, got)
	require.Len(t, got.GetRules(), 1)
	assert.Equal(t, "https", got.GetRules()[0].GetProto(), "tls alias must be rewritten to https")
}
