package hostgateway_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/controlplane/hostgateway"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

func agentA() hostgateway.Identity {
	return hostgateway.Identity{Project: "proj", Agent: "a", ContainerID: "ctr-a"}
}

func agentB() hostgateway.Identity {
	return hostgateway.Identity{Project: "proj", Agent: "b", ContainerID: "ctr-b"}
}

// cpServer serves the gateway as HostProxyService over mTLS with the real
// auth material: the CP server leaf, client certs chained to the CLI CA.
type cpServer struct {
	gw   *hostgateway.Gateway
	lis  *bufconn.Listener
	pool *x509.CertPool
}

func newCPServer(t *testing.T) *cpServer {
	t.Helper()
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())

	caCert, err := auth.CACert()
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	certPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	keyPath, err := consts.AuthServerKeyPath()
	require.NoError(t, err)
	serverCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	gw := hostgateway.New(logger.Nop())
	var serverTLS tls.Config
	serverTLS.Certificates = []tls.Certificate{serverCert}
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	serverTLS.ClientCAs = pool
	serverTLS.MinVersion = tls.VersionTLS13
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&serverTLS)))
	hostproxyv1.RegisterHostProxyServiceServer(srv, gw)
	lis := bufconn.Listen(1 << 20)
	go func() {
		if serveErr := srv.Serve(lis); serveErr != nil {
			t.Logf("grpc serve: %v", serveErr)
		}
	}()
	t.Cleanup(srv.Stop)
	return &cpServer{gw: gw, lis: lis, pool: pool}
}

// open dials the gateway with cert and opens a Connect stream. It returns
// the host end once the CP has accepted the stream (the response header);
// the caller starts serve.
func (s *cpServer) open(t *testing.T, cert tls.Certificate) (*hostProxy, error) {
	t.Helper()
	var clientTLS tls.Config
	clientTLS.RootCAs = s.pool
	clientTLS.ServerName = consts.ContainerCP
	clientTLS.Certificates = []tls.Certificate{cert}
	clientTLS.MinVersion = tls.VersionTLS13
	conn, err := grpc.NewClient("passthrough:///cp",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return s.lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(credentials.NewTLS(&clientTLS)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Logf("close conn: %v", closeErr)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := hostproxyv1.NewHostProxyServiceClient(conn).Connect(ctx)
	if err != nil {
		return nil, err
	}
	md, err := stream.Header()
	if err != nil {
		return nil, err
	}
	if len(md.Get(hostproxyv1.AcceptedHeader)) == 0 {
		_, err = stream.Recv()
		return nil, err
	}
	return &hostProxy{
		stream:   stream,
		mu:       sync.Mutex{},
		requests: nil,
		replies:  nil,
		hold:     false,
		done:     make(chan error, 1),
	}, nil
}

// hostProxy is the host end of a Connect stream: it records each request
// and answers with the reply the test sets for the action, or an empty
// success.
type hostProxy struct {
	stream hostproxyv1.HostProxyService_ConnectClient

	mu       sync.Mutex
	requests []*hostproxyv1.HostActionRequest
	replies  map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult
	hold     bool // when set, record requests but never answer
	done     chan error
}

func connectHostProxy(
	t *testing.T, s *cpServer, replies map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult,
) *hostProxy {
	t.Helper()
	cert, err := auth.LoadHostProxyClientCert()
	require.NoError(t, err)
	hp, err := s.open(t, cert)
	require.NoError(t, err)
	hp.replies = replies
	go hp.serve()
	return hp
}

func (hp *hostProxy) serve() {
	for {
		req, err := hp.stream.Recv()
		if err != nil {
			hp.done <- err
			return
		}
		hp.mu.Lock()
		hp.requests = append(hp.requests, req)
		reply, hold := hp.replies[kind(req)], hp.hold
		hp.mu.Unlock()
		if hold {
			continue
		}
		res := success(req)
		if reply != nil {
			res = reply(req)
		}
		res.Id = req.GetId()
		if sendErr := hp.stream.Send(res); sendErr != nil {
			hp.done <- sendErr
			return
		}
	}
}

func (hp *hostProxy) calls() []*hostproxyv1.HostActionRequest {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	return append([]*hostproxyv1.HostActionRequest(nil), hp.requests...)
}

func (hp *hostProxy) setHold() {
	hp.mu.Lock()
	hp.hold = true
	hp.mu.Unlock()
}

func kind(req *hostproxyv1.HostActionRequest) string {
	switch req.GetAction().(type) {
	case *hostproxyv1.HostActionRequest_OpenUrl:
		return "open_url"
	case *hostproxyv1.HostActionRequest_GitCredential:
		return "git_credential"
	case *hostproxyv1.HostActionRequest_RegisterCallback:
		return "register_callback"
	case *hostproxyv1.HostActionRequest_GetCallbackData:
		return "get_callback_data"
	case *hostproxyv1.HostActionRequest_DeleteCallback:
		return "delete_callback"
	}
	return ""
}

// success is the empty success result for the request's action.
func success(req *hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
	res := new(hostproxyv1.HostActionResult)
	switch req.GetAction().(type) {
	case *hostproxyv1.HostActionRequest_OpenUrl:
		res.Outcome = &hostproxyv1.HostActionResult_OpenUrl{OpenUrl: new(agentv1.OpenURLResult)}
	case *hostproxyv1.HostActionRequest_GitCredential:
		res.Outcome = &hostproxyv1.HostActionResult_GitCredential{GitCredential: new(agentv1.GitCredentialResult)}
	case *hostproxyv1.HostActionRequest_RegisterCallback:
		res.Outcome = &hostproxyv1.HostActionResult_RegisterCallback{
			RegisterCallback: new(agentv1.RegisterCallbackResult),
		}
	case *hostproxyv1.HostActionRequest_GetCallbackData:
		res.Outcome = &hostproxyv1.HostActionResult_GetCallbackData{GetCallbackData: new(agentv1.GetCallbackDataResult)}
	case *hostproxyv1.HostActionRequest_DeleteCallback:
		res.Outcome = &hostproxyv1.HostActionResult_DeleteCallback{DeleteCallback: new(agentv1.DeleteCallbackResult)}
	}
	return res
}

func failure(code codes.Code) func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
	return func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
		return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_Error{
			Error: &hostproxyv1.HostActionError{Code: uint32(code), Message: "no"},
		}}
	}
}

func TestGateway_NoHostProxy_Unavailable(t *testing.T) {
	s := newCPServer(t)
	err := s.gw.OpenURL(context.Background(), agentA(), "https://example.com")
	require.ErrorIs(t, err, hostgateway.ErrUnavailable)
}

// TestGateway_RejectsOtherClientCerts: the stream is the host proxy's
// lane only. A CLI-CA leaf with another CN (the CLI's own) is refused.
func TestGateway_RejectsOtherClientCerts(t *testing.T) {
	s := newCPServer(t)
	cliCert, err := auth.LoadClientCert()
	require.NoError(t, err)

	_, err = s.open(t, cliCert)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	err = s.gw.OpenURL(context.Background(), agentA(), "https://example.com")
	require.ErrorIs(t, err, hostgateway.ErrUnavailable, "a refused stream must not serve host actions")
}

func TestGateway_OpenURL_Forwards(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, nil)

	require.NoError(t, s.gw.OpenURL(context.Background(), agentA(), "https://example.com/x"))

	calls := hp.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "https://example.com/x", calls[0].GetOpenUrl().GetUrl())
}

// TestGateway_StatusMapping pins how a host-proxy verdict reaches the
// handler.
func TestGateway_StatusMapping(t *testing.T) {
	cases := []struct {
		name string
		code codes.Code
		want error
	}{
		{"egress deny", codes.PermissionDenied, hostgateway.ErrDenied},
		{"bad request", codes.InvalidArgument, hostgateway.ErrInvalidRequest},
		{"no session", codes.NotFound, hostgateway.ErrNotFound},
		{"helper failed", codes.FailedPrecondition, hostgateway.ErrActionFailed},
		{"host fault", codes.Internal, hostgateway.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newCPServer(t)
			connectHostProxy(t, s, map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult{
				"open_url": failure(tc.code),
			})
			err := s.gw.OpenURL(context.Background(), agentA(), "https://example.com")
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestGateway_WrongResultKind: a result that does not match the request
// is a host-proxy fault, never a success.
func TestGateway_WrongResultKind(t *testing.T) {
	s := newCPServer(t)
	connectHostProxy(t, s, map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult{
		"git_credential": func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
			return &hostproxyv1.HostActionResult{
				Id:      0,
				Outcome: &hostproxyv1.HostActionResult_OpenUrl{OpenUrl: new(agentv1.OpenURLResult)},
			}
		},
	})
	_, err := s.gw.GitCredential(context.Background(), agentA(), gitGet())
	require.ErrorIs(t, err, hostgateway.ErrUnavailable)
}

func TestGateway_GitCredential(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult{
		"git_credential": func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
			return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_GitCredential{
				GitCredential: &agentv1.GitCredentialResult{
					Protocol: "https",
					Host:     "github.com",
					Username: "u",
					Password: "p",
				},
			}}
		},
	})

	res, err := s.gw.GitCredential(context.Background(), agentA(), gitGet())
	require.NoError(t, err)
	assert.Equal(t, "u", res.GetUsername())
	assert.Equal(t, "p", res.GetPassword())

	calls := hp.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET, calls[0].GetGitCredential().GetAction())
	assert.Equal(t, "github.com", calls[0].GetGitCredential().GetHost())
}

func gitGet() *agentv1.GitCredentialRequest {
	return &agentv1.GitCredentialRequest{
		Action:   agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
		Protocol: "https",
		Host:     "github.com",
		Path:     "",
		Username: "",
		Password: "",
	}
}

func registerReply(
	sessionID string,
	expires time.Time,
) func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
	return func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
		return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_RegisterCallback{
			RegisterCallback: &agentv1.RegisterCallbackResult{
				SessionId:     sessionID,
				Port:          54545,
				ExpiresAtUnix: expires.Unix(),
			},
		}}
	}
}

// TestGateway_CallbackOwnership: a callback session belongs to the
// container that registered it. Another container cannot read or delete
// it, and its requests never reach the host proxy.
func TestGateway_CallbackOwnership(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult{
		"register_callback": registerReply("s1", time.Now().Add(5*time.Minute)),
		"get_callback_data": func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
			return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_GetCallbackData{
				GetCallbackData: &agentv1.GetCallbackDataResult{Received: true, Callback: &agentv1.CallbackData{
					Method: "GET", Path: "/cb", Query: "code=x", Headers: nil, Body: "", ReceivedAtUnix: 1700000000,
				}},
			}}
		},
	})
	gw, ctx := s.gw, context.Background()
	const authURL = "https://auth.example.com/?redirect_uri=http://localhost:54545/cb"

	reg, err := gw.RegisterCallback(ctx, agentA(), &agentv1.RegisterCallbackRequest{AuthUrl: authURL, TtlSeconds: 60})
	require.NoError(t, err)
	assert.Equal(t, "s1", reg.GetSessionId())
	assert.Equal(t, int32(54545), reg.GetPort())
	first := hp.calls()[0].GetRegisterCallback()
	assert.Equal(t, authURL, first.GetAuthUrl())
	assert.Equal(t, int32(60), first.GetTtlSeconds())

	_, err = gw.GetCallbackData(ctx, agentB(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
	require.ErrorIs(t, gw.DeleteCallback(ctx, agentB(), "s1"), hostgateway.ErrNotOwner)
	require.Len(t, hp.calls(), 1, "a non-owner request must not reach the host proxy")

	data, err := gw.GetCallbackData(ctx, agentA(), "s1")
	require.NoError(t, err)
	require.True(t, data.GetReceived())
	assert.Equal(t, "code=x", data.GetCallback().GetQuery())
	assert.Equal(t, "s1", hp.calls()[1].GetGetCallbackData().GetSessionId())

	require.NoError(t, gw.DeleteCallback(ctx, agentA(), "s1"))
	assert.Equal(t, "s1", hp.calls()[2].GetDeleteCallback().GetSessionId())
	_, err = gw.GetCallbackData(ctx, agentA(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner, "a deleted session has no owner")
}

func TestGateway_CallbackUnknownSession(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, nil)
	_, err := s.gw.GetCallbackData(context.Background(), agentA(), "never-registered")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
	assert.Empty(t, hp.calls())
}

// TestGateway_CallbackOwnerExpires: the ownership record ends with the
// session lifetime the host proxy reported.
func TestGateway_CallbackOwnerExpires(t *testing.T) {
	expires := time.Unix(1700000300, 0)
	s := newCPServer(t)
	connectHostProxy(t, s, map[string]func(*hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult{
		"register_callback": registerReply("s1", expires),
	})
	now := time.Unix(1700000000, 0)
	s.gw.Clock = func() time.Time { return now }
	ctx := context.Background()

	_, err := s.gw.RegisterCallback(
		ctx,
		agentA(),
		&agentv1.RegisterCallbackRequest{AuthUrl: "https://a.example.com", TtlSeconds: 0},
	)
	require.NoError(t, err)

	now = expires.Add(time.Second)
	_, err = s.gw.GetCallbackData(ctx, agentA(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
}

// TestGateway_NewestStreamWins: a second host proxy stream replaces the
// first. The first stream ends, its call in flight fails as unavailable,
// and new calls go to the second stream.
func TestGateway_NewestStreamWins(t *testing.T) {
	s := newCPServer(t)
	old := connectHostProxy(t, s, nil)
	old.setHold()

	inflight := make(chan error, 1)
	go func() { inflight <- s.gw.OpenURL(context.Background(), agentA(), "https://old.example.com") }()
	require.Eventually(t, func() bool { return len(old.calls()) == 1 }, 5*time.Second, 10*time.Millisecond)

	fresh := connectHostProxy(t, s, nil)

	require.ErrorIs(t, <-inflight, hostgateway.ErrUnavailable)
	select {
	case err := <-old.done:
		assert.Equal(t, codes.Aborted, status.Code(err), "the replaced stream must end as replaced: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced stream did not end")
	}

	require.NoError(t, s.gw.OpenURL(context.Background(), agentA(), "https://new.example.com"))
	require.Len(t, fresh.calls(), 1)
	assert.Equal(t, "https://new.example.com", fresh.calls()[0].GetOpenUrl().GetUrl())
}

// TestGateway_StreamDropFailsPending: when the host proxy goes away, a
// call in flight fails as unavailable and later calls do too.
func TestGateway_StreamDropFailsPending(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, nil)
	hp.setHold()

	inflight := make(chan error, 1)
	go func() { inflight <- s.gw.OpenURL(context.Background(), agentA(), "https://example.com") }()
	require.Eventually(t, func() bool { return len(hp.calls()) == 1 }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, hp.stream.CloseSend())
	require.ErrorIs(t, <-inflight, hostgateway.ErrUnavailable)
	require.ErrorIs(t, s.gw.OpenURL(context.Background(), agentA(), "https://example.com"), hostgateway.ErrUnavailable)
}

// TestGateway_CallerDeadline: a host proxy that never answers does not
// hold the agent's call past its deadline.
func TestGateway_CallerDeadline(t *testing.T) {
	s := newCPServer(t)
	hp := connectHostProxy(t, s, nil)
	hp.setHold()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.gw.OpenURL(ctx, agentA(), "https://example.com"), hostgateway.ErrUnavailable)
}
