package controlplane_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	cpfw "github.com/schmitthub/clawker/internal/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf/mocks"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// newTestServer creates a gRPC server with AuthInterceptor + AdminHandler
// backed by mock introspector and mock ebpf manager. Returns a connected
// AdminService client.
func newTestServer(t *testing.T, introspector *cpmocks.IntrospectorMock, ebpfMgr *ebpfmocks.EBPFManagerMock) adminv1.AdminServiceClient {
	t.Helper()

	log := logger.Nop()
	interceptor := controlplane.NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)

	queue := cpfw.NewActionQueue(log)
	t.Cleanup(func() { _ = queue.Close() })
	handler := cpfw.NewHandler(cpfw.HandlerDeps{
		EBPF:     ebpfMgr,
		Resolver: nopContainerResolver,
		Log:      log,
		Queue:    queue,
	})
	adminv1.RegisterAdminServiceServer(srv, handler)

	lis := bufconnListen(t)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(bufconnDialer(lis)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return adminv1.NewAdminServiceClient(conn)
}

// allowAllIntrospector returns a mock that marks every token as active
// with admin scope.
func allowAllIntrospector() *cpmocks.IntrospectorMock {
	return &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return &controlplane.IntrospectionResult{
				Active:   true,
				Scope:    "admin",
				ClientID: consts.ClientIDCLI,
				Sub:      consts.ClientIDCLI,
			}, nil
		},
	}
}

// denyAllIntrospector returns a mock that rejects every token.
func denyAllIntrospector() *cpmocks.IntrospectorMock {
	return &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return &controlplane.IntrospectionResult{Active: false}, nil
		},
	}
}

// noopEBPF returns a mock ebpf manager where all methods succeed.
func noopEBPF() *ebpfmocks.EBPFManagerMock {
	return &ebpfmocks.EBPFManagerMock{
		InstallFunc:    func(_ uint64, _ string, _ ebpf.BPFContainerConfig) error { return nil },
		RemoveFunc:     func(_ uint64) error { return nil },
		EnableFunc:     func(_ uint64) error { return nil },
		DisableFunc:    func(_ uint64) error { return nil },
		SyncRoutesFunc: func(_ []ebpf.Route) error { return nil },
		FlushAllFunc:   func() error { return nil },
	}
}

// --- Interceptor tests ---

func TestAuthInterceptor_NoToken_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	// Non-public method without a token must be denied.
	_, err := client.FirewallEnable(context.Background(), &adminv1.FirewallEnableRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Empty(t, introspector.IntrospectCalls())
}

func TestAuthInterceptor_InvalidToken_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "bad-token")
	_, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	calls := introspector.IntrospectCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "bad-token", calls[0].Token)
}

func TestAuthInterceptor_ValidToken_WrongScope_Denied(t *testing.T) {
	// Scope-aware mock: token is valid but only carries "agent:read",
	// not the "admin" scope SyncRoutes requires. Hydra returns
	// active=false when the requested scope isn't granted.
	introspector := &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, requiredScope string) (*controlplane.IntrospectionResult, error) {
			granted := "agent:read"
			return &controlplane.IntrospectionResult{
				Active:   requiredScope == granted,
				Scope:    granted,
				ClientID: consts.ClientIDAgent,
				Sub:      consts.ClientIDAgent,
			}, nil
		},
	}
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "agent-token")
	_, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	calls := introspector.IntrospectCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "agent-token", calls[0].Token, "token must be forwarded to introspector")
	assert.Equal(t, "admin", calls[0].RequiredScope, "SyncRoutes requires admin scope")
}

func TestAuthInterceptor_ValidToken_CorrectScope_Allowed(t *testing.T) {
	introspector := allowAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "admin-token")
	resp, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, introspector.IntrospectCalls(), 1)
}

func TestAuthInterceptor_UnmappedMethod_Denied(t *testing.T) {
	introspector := allowAllIntrospector()

	log := logger.Nop()
	// Empty scope map — nothing is mapped.
	interceptor := controlplane.NewAuthInterceptor(introspector, map[string]adminv1.AdminScope{}, log)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
	)
	queue := cpfw.NewActionQueue(log)
	t.Cleanup(func() { _ = queue.Close() })
	handler := cpfw.NewHandler(cpfw.HandlerDeps{
		EBPF:     noopEBPF(),
		Resolver: nopContainerResolver,
		Log:      log,
		Queue:    queue,
	})
	adminv1.RegisterAdminServiceServer(srv, handler)

	lis := bufconnListen(t)
	go func() { srv.Serve(lis) }() //nolint:errcheck
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(bufconnDialer(lis)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "admin-token")
	_, err = client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"unmapped methods must be denied (fail-closed)")
	assert.Empty(t, introspector.IntrospectCalls())
}

// TestAuthInterceptor_PublicScope_PublicMethod_NoToken_Allowed pins the public
// branch that GetSystemTime relies on for token-exchange bootstrap: a method
// mapped to consts.ScopePublic must be served on mTLS alone, with NO bearer
// token, and the introspector must never be consulted. A regression that
// treated the public scope as deny — or still demanded a token — would break
// the CLI's clock-skew probe, and TestAdminMethodScopes_CoversAllRPCs (which
// only checks map keys) would not catch it.
//
// The load-bearing proof is the deny-all introspector tripwire: if auth fell
// through to introspection it would reject, so an empty IntrospectCalls() means
// the public arm short-circuited before introspection. The error-code check
// only pins that auth did not itself reject — it deliberately does NOT assert
// the downstream handler outcome, which is a harness artifact here
// (newTestServer registers the bare firewall Handler, whose
// UnimplementedAdminServiceServer answers GetSystemTime; the production
// adminServer returns a real time).
func TestAuthInterceptor_PublicScope_PublicMethod_NoToken_Allowed(t *testing.T) {
	// GetSystemTime must be mapped to the public scope — the precondition for
	// this whole branch.
	require.Equal(t, consts.ScopePublic, string(adminv1.AdminMethodScopes()["/"+adminv1.ServiceName+"/GetSystemTime"]),
		"GetSystemTime must be public (consts.ScopePublic)")

	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	// No bearer token on the context at all.
	_, err := client.GetSystemTime(context.Background(), &adminv1.GetSystemTimeRequest{})
	if err != nil {
		code := status.Code(err)
		assert.NotEqual(t, codes.Unauthenticated, code,
			"public-scope method must not be rejected by auth")
		assert.NotEqual(t, codes.PermissionDenied, code,
			"public-scope method must not be rejected by auth")
	}
	assert.Empty(t, introspector.IntrospectCalls(),
		"a public-scope method must not consult the introspector")
}

func TestAuthInterceptor_IntrospectionError_Denied(t *testing.T) {
	introspector := &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "any-token")
	_, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"introspection failure must deny (fail-closed)")
}

func TestAuthInterceptor_MalformedAuthHeader_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	ctx := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Basic YWRtaW46cGFzc3dvcmQ=")
	_, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Empty(t, introspector.IntrospectCalls())
}

// --- Handler tests (auth passes, exercises handler logic) ---
//
// The old FirewallSyncRoutes tests asserted "caller-supplied routes are
// forwarded to ebpf.SyncRoutes verbatim". That semantic is gone — the
// RPC now goes through the queue and the closure rebuilds routes from
// the rules store (see internal/controlplane/firewall.FirewallSyncRoutes).
// Route mapping coverage moved to the firewall handler test package
// where a store can be wired; this file retains only the auth passes.

func TestAdminHandler_SyncRoutes_AuthPasses(t *testing.T) {
	ebpfMgr := noopEBPF()
	client := newTestServer(t, allowAllIntrospector(), ebpfMgr)

	ctx := withBearer(context.Background(), "admin-token")
	_, err := client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.NoError(t, err)
}

// --- Coverage test ---

func TestAdminMethodScopes_CoversAllRPCs(t *testing.T) {
	scopes := adminv1.AdminMethodScopes()
	desc := adminv1.AdminService_ServiceDesc

	// Collect all methods from the proto service descriptor (unary + streams).
	protoMethods := make(map[string]struct{})
	for _, m := range desc.Methods {
		protoMethods["/"+desc.ServiceName+"/"+m.MethodName] = struct{}{}
	}
	for _, s := range desc.Streams {
		protoMethods["/"+desc.ServiceName+"/"+s.StreamName] = struct{}{}
	}

	// Every proto RPC must have a scope entry (prevents missing auth on new RPCs).
	for method := range protoMethods {
		_, ok := scopes[method]
		assert.True(t, ok,
			"proto RPC %s has no scope in AdminMethodScopes() — add an entry to enforce auth", method)
	}

	// Every scope entry must correspond to a real proto RPC (prevents stale entries).
	for method := range scopes {
		_, ok := protoMethods[method]
		assert.True(t, ok,
			"AdminMethodScopes() contains %s which is not in AdminService_ServiceDesc — remove stale entry", method)
	}

	// Exact count match as a belt-and-suspenders check.
	assert.Equal(t, len(protoMethods), len(scopes),
		"AdminMethodScopes() count (%d) must equal proto RPC count (%d)", len(scopes), len(protoMethods))
}

// TestScopeTypesAreDistinct is the runtime backstop for the compile-time
// cross-service guard: AdminScope and AgentScope must be DISTINCT named
// types, not aliases of one shared type. The compiler already rejects an
// agent scope in AdminMethodScopes (and vice versa) precisely because the
// types differ — but a future refactor that collapsed them back into
// `type AdminScope = SharedScope` aliases would silently restore the
// "buffet of scopes" hole without breaking any other test. This asserts
// the property reflectively so that regression fails loudly: if the two
// reflect.Types are equal, the distinctness (and the guard) is gone.
func TestScopeTypesAreDistinct(t *testing.T) {
	adminT := reflect.TypeFor[adminv1.AdminScope]()
	agentT := reflect.TypeFor[agentv1.AgentScope]()
	assert.NotEqual(t, adminT, agentT,
		"AdminScope (%s) and AgentScope (%s) must be distinct types, not aliases — "+
			"distinctness is what stops a cross-service scope from being wired into the wrong service",
		adminT, agentT)
}

// --- RequireClientID (agent-listener pin) ---
//
// The agent gRPC listener pins its AuthInterceptor to
// consts.ClientIDAgent so a token whose scope is correct but whose
// client_id is not the agent's still fails closed. These tests don't
// boot the agent service (no AgentService stub here) — they exercise
// the interceptor against the AdminService surface with a synthetic
// scope→client_id mismatch, which is sufficient to lock the behavior
// at the interceptor boundary.

func TestAuthInterceptor_RequireClientID_Mismatch_Denied(t *testing.T) {
	// Token has the right scope ("admin") but a client_id the
	// interceptor doesn't accept. Must be denied with PermissionDenied
	// — same generic error code/message as the missing-scope path so
	// callers can't tell which check failed.
	introspector := &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return &controlplane.IntrospectionResult{
				Active:   true,
				Scope:    "admin",
				ClientID: "some-other-client",
				Sub:      "some-other-client",
			}, nil
		},
	}

	log := logger.Nop()
	interceptor := controlplane.
		NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log).
		RequireClientID(consts.ClientIDCLI)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)
	queue := cpfw.NewActionQueue(log)
	t.Cleanup(func() { _ = queue.Close() })
	handler := cpfw.NewHandler(cpfw.HandlerDeps{
		EBPF:     noopEBPF(),
		Resolver: nopContainerResolver,
		Log:      log,
		Queue:    queue,
	})
	adminv1.RegisterAdminServiceServer(srv, handler)

	lis := bufconnListen(t)
	go func() { srv.Serve(lis) }() //nolint:errcheck
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(bufconnDialer(lis)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "wrong-client-token")
	_, err = client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err),
		"client_id mismatch must be denied even with correct scope")
}

func TestAuthInterceptor_RequireClientID_Match_Allowed(t *testing.T) {
	// Same setup, but the token's client_id matches — request goes
	// through. Anchors the positive path so a future regression that
	// over-rejects (e.g. compares the wrong field) is caught.
	introspector := &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return &controlplane.IntrospectionResult{
				Active:   true,
				Scope:    "admin",
				ClientID: consts.ClientIDCLI,
				Sub:      consts.ClientIDCLI,
			}, nil
		},
	}

	log := logger.Nop()
	interceptor := controlplane.
		NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log).
		RequireClientID(consts.ClientIDCLI)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)
	queue := cpfw.NewActionQueue(log)
	t.Cleanup(func() { _ = queue.Close() })
	handler := cpfw.NewHandler(cpfw.HandlerDeps{
		EBPF:     noopEBPF(),
		Resolver: nopContainerResolver,
		Log:      log,
		Queue:    queue,
	})
	adminv1.RegisterAdminServiceServer(srv, handler)

	lis := bufconnListen(t)
	go func() { srv.Serve(lis) }() //nolint:errcheck
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(bufconnDialer(lis)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "right-client-token")
	_, err = client.FirewallSyncRoutes(ctx, &adminv1.FirewallSyncRoutesRequest{})
	require.NoError(t, err)
}

// --- Helpers ---

func withBearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
