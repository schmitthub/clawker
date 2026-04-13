package controlplane_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/controlplane"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/ebpf/mocks"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// newTestServer creates a gRPC server with AuthInterceptor + AdminHandler
// backed by mock introspector and mock ebpf manager. Returns a connected
// AdminService client.
func newTestServer(t *testing.T, introspector *cpmocks.IntrospectorMock, ebpfMgr *ebpfmocks.EBPFManagerMock) adminv1.AdminServiceClient {
	t.Helper()

	log := logger.Nop()
	interceptor := controlplane.NewAuthInterceptor(introspector, controlplane.AdminMethodScopes(), log)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)

	handler := controlplane.NewAdminHandler(ebpfMgr, log)
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
				ClientID: "clawker-cli",
				Sub:      "clawker-cli",
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
	}
}

// --- Interceptor tests ---

func TestAuthInterceptor_NoToken_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	// Non-public method without a token must be denied.
	_, err := client.Install(context.Background(), &adminv1.InstallRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Empty(t, introspector.IntrospectCalls())
}

func TestAuthInterceptor_InvalidToken_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "bad-token")
	_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
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
				ClientID: "clawker-agent",
				Sub:      "clawker-agent",
			}, nil
		},
	}
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "agent-token")
	_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
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
	resp, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, introspector.IntrospectCalls(), 1)
}

func TestAuthInterceptor_UnmappedMethod_Denied(t *testing.T) {
	introspector := allowAllIntrospector()

	log := logger.Nop()
	// Empty scope map — nothing is mapped.
	interceptor := controlplane.NewAuthInterceptor(introspector, map[string]string{}, log)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
	)
	handler := controlplane.NewAdminHandler(noopEBPF(), log)
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
	_, err = client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"unmapped methods must be denied (fail-closed)")
	assert.Empty(t, introspector.IntrospectCalls())
}

func TestAuthInterceptor_IntrospectionError_Denied(t *testing.T) {
	introspector := &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	client := newTestServer(t, introspector, noopEBPF())

	ctx := withBearer(context.Background(), "any-token")
	_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"introspection failure must deny (fail-closed)")
}

func TestAuthInterceptor_MalformedAuthHeader_Denied(t *testing.T) {
	introspector := denyAllIntrospector()
	client := newTestServer(t, introspector, noopEBPF())

	ctx := metadata.AppendToOutgoingContext(context.Background(),
		"authorization", "Basic YWRtaW46cGFzc3dvcmQ=")
	_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Empty(t, introspector.IntrospectCalls())
}

// --- Handler tests (auth passes, exercises handler logic) ---

func TestAdminHandler_SyncRoutes(t *testing.T) {
	ebpfMgr := noopEBPF()
	client := newTestServer(t, allowAllIntrospector(), ebpfMgr)

	ctx := withBearer(context.Background(), "admin-token")
	resp, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{
		Routes: []*adminv1.Route{
			{DomainHash: 12345, DstPort: 443, EnvoyPort: 10000},
			{DomainHash: 67890, DstPort: 80, EnvoyPort: 10000},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(2), resp.GetApplied())

	// Verify proto→domain field mapping — the real bug surface here.
	calls := ebpfMgr.SyncRoutesCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, []ebpf.Route{
		{DomainHash: 12345, DstPort: 443, EnvoyPort: 10000},
		{DomainHash: 67890, DstPort: 80, EnvoyPort: 10000},
	}, calls[0].Routes)
}

func TestAdminHandler_SyncRoutes_EBPFError(t *testing.T) {
	ebpfMgr := noopEBPF()
	ebpfMgr.SyncRoutesFunc = func(_ []ebpf.Route) error {
		return fmt.Errorf("map full")
	}
	client := newTestServer(t, allowAllIntrospector(), ebpfMgr)

	ctx := withBearer(context.Background(), "admin-token")
	_, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{
		Routes: []*adminv1.Route{{DomainHash: 1, DstPort: 443, EnvoyPort: 10000}},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// --- Coverage test ---

func TestAdminMethodScopes_CoversAllRPCs(t *testing.T) {
	scopes := controlplane.AdminMethodScopes()
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

// --- Helpers ---

func withBearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
