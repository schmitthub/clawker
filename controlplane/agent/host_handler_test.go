package agent_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	agentmocks "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/schmitthub/clawker/controlplane/hostgateway"
	"github.com/schmitthub/clawker/internal/consts"
)

// hostFixture is a registered container with host-action grants and a
// handler wired with a recording host gateway.
type hostFixture struct {
	leaf     *x509.Certificate
	resolved agent.ResolvedContainer
	gateway  *agentmocks.HostForwarderMock
	handler  *agent.Handler
}

func newHostFixture(t *testing.T, hostProxy, gitHTTPS bool) *hostFixture {
	t.Helper()
	caCert, caKey := genTestCA(t)
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd, sanFullName, "ctr-id")
	resolved := resolvedFor(t, project, "ctr-id")
	resolved.HostProxy = hostProxy
	resolved.GitHTTPS = gitHTTPS
	thumb := sha256.Sum256(leaf.Raw)
	registry := new(agentmocks.RegistryMock)
	registry.LookupByContainerIDFunc = func(containerID string) (*agent.Entry, error) {
		e := new(agent.Entry)
		e.ContainerID = containerID
		e.Thumbprint = thumb
		return e, nil
	}
	gw := &agentmocks.HostForwarderMock{
		OpenURLFunc: func(context.Context, hostgateway.Identity, string) error { return nil },
		GitCredentialFunc: func(context.Context, hostgateway.Identity, *agentv1.GitCredentialRequest) (*agentv1.GitCredentialResult, error) {
			return &agentv1.GitCredentialResult{
				Protocol: "https", Host: "github.com", Username: "u", Password: "p",
			}, nil
		},
		RegisterCallbackFunc: func(context.Context, hostgateway.Identity, *agentv1.RegisterCallbackRequest) (*agentv1.RegisterCallbackResult, error) {
			return &agentv1.RegisterCallbackResult{SessionId: "s1", Port: 54545, ExpiresAtUnix: 0}, nil
		},
		GetCallbackDataFunc: func(context.Context, hostgateway.Identity, string) (*agentv1.GetCallbackDataResult, error) {
			return &agentv1.GetCallbackDataResult{}, nil
		},
		DeleteCallbackFunc: func(context.Context, hostgateway.Identity, string) error { return nil },
	}
	h := newTestHandler(registry)
	h.HostGateway = gw
	return &hostFixture{leaf: leaf, resolved: resolved, gateway: gw, handler: h}
}

// ctx carries the peer cert and the resolved identity, the state the
// interceptor leaves for the handler.
func (f *hostFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return resolvedCtx(t, f.leaf, f.resolved)
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

// hostCalls runs every host action once against f and returns the error
// of each, keyed by RPC name.
func hostCalls(t *testing.T, f *hostFixture) map[string]error {
	t.Helper()
	ctx := f.ctx(t)
	_, gitErr := f.handler.GitCredential(ctx, gitGet())
	regReq := &agentv1.RegisterCallbackRequest{AuthUrl: "https://a.example.com", TtlSeconds: 0}
	_, regErr := f.handler.RegisterCallback(ctx, regReq)
	_, getErr := f.handler.GetCallbackData(ctx, &agentv1.GetCallbackDataRequest{SessionId: "s1"})
	_, delErr := f.handler.DeleteCallback(ctx, &agentv1.DeleteCallbackRequest{SessionId: "s1"})
	_, openErr := f.handler.OpenURL(ctx, &agentv1.OpenURLRequest{Url: "https://a.example.com"})
	return map[string]error{
		"OpenURL": openErr, "GitCredential": gitErr, "RegisterCallback": regErr,
		"GetCallbackData": getErr, "DeleteCallback": delErr,
	}
}

func gatewayCallCount(gw *agentmocks.HostForwarderMock) int {
	return len(gw.OpenURLCalls()) + len(gw.GitCredentialCalls()) + len(gw.RegisterCallbackCalls()) +
		len(gw.GetCallbackDataCalls()) + len(gw.DeleteCallbackCalls())
}

// TestHostActions_Granted: a container with both grants reaches the
// gateway on every action, with the CP-resolved identity.
func TestHostActions_Granted(t *testing.T) {
	f := newHostFixture(t, true, true)
	for rpc, err := range hostCalls(t, f) {
		require.NoError(t, err, rpc)
	}
	require.Len(t, f.gateway.OpenURLCalls(), 1)
	assert.Equal(t, hostgateway.Identity{Project: project, Agent: agentName, ContainerID: "ctr-id"},
		f.gateway.OpenURLCalls()[0].ID)
	assert.Equal(t, "https://a.example.com", f.gateway.OpenURLCalls()[0].RawURL)
	assert.Equal(t, "s1", f.gateway.GetCallbackDataCalls()[0].SessionID)
}

// TestHostActions_HostProxyNotGranted: without the host-proxy grant no
// action reaches the host.
func TestHostActions_HostProxyNotGranted(t *testing.T) {
	f := newHostFixture(t, false, true)
	for _, err := range hostCalls(t, f) {
		assertCode(t, err, codes.PermissionDenied)
	}
	assert.Zero(t, gatewayCallCount(f.gateway))
}

// TestHostActions_GitHTTPSNotGranted: the git credential lane needs its
// own grant; the other actions stay available.
func TestHostActions_GitHTTPSNotGranted(t *testing.T) {
	f := newHostFixture(t, true, false)
	errs := hostCalls(t, f)
	assertCode(t, errs["GitCredential"], codes.PermissionDenied)
	assert.Empty(t, f.gateway.GitCredentialCalls())
	require.NoError(t, errs["OpenURL"])
}

// TestHostActions_UnregisteredPeer: the registry thumbprint check runs
// before any grant or gateway work.
func TestHostActions_UnregisteredPeer(t *testing.T) {
	f := newHostFixture(t, true, true)
	registry := new(agentmocks.RegistryMock)
	registry.LookupByContainerIDFunc = func(string) (*agent.Entry, error) { return nil, agent.ErrUnknownAgent }
	f.handler.Registry = registry
	for _, err := range hostCalls(t, f) {
		assertCode(t, err, codes.PermissionDenied)
	}
	assert.Zero(t, gatewayCallCount(f.gateway))
}

func TestHostActions_NilGateway(t *testing.T) {
	f := newHostFixture(t, true, true)
	f.handler.HostGateway = nil
	for _, err := range hostCalls(t, f) {
		assertCode(t, err, codes.Unavailable)
	}
}

// TestHostActions_RequestValidation: malformed requests are rejected
// before identity or gateway work.
func TestHostActions_RequestValidation(t *testing.T) {
	f := newHostFixture(t, true, true)
	h := f.handler
	ctx := f.ctx(t)
	_, err := h.OpenURL(ctx, &agentv1.OpenURLRequest{Url: ""})
	assertCode(t, err, codes.InvalidArgument)
	noAction := gitGet()
	noAction.Action = agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED
	_, err = h.GitCredential(ctx, noAction)
	assertCode(t, err, codes.InvalidArgument)
	_, err = h.RegisterCallback(ctx, &agentv1.RegisterCallbackRequest{AuthUrl: "", TtlSeconds: 0})
	assertCode(t, err, codes.InvalidArgument)
	_, err = h.GetCallbackData(ctx, &agentv1.GetCallbackDataRequest{SessionId: ""})
	assertCode(t, err, codes.InvalidArgument)
	_, err = h.DeleteCallback(ctx, &agentv1.DeleteCallbackRequest{SessionId: ""})
	assertCode(t, err, codes.InvalidArgument)
	_, err = h.OpenURL(ctx, nil)
	assertCode(t, err, codes.InvalidArgument)
	assert.Zero(t, gatewayCallCount(f.gateway))
}

// TestHostActions_GatewayErrorMapping pins the gateway sentinel to status
// mapping the clawkerd relay turns into HTTP answers.
func TestHostActions_GatewayErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{hostgateway.ErrNotOwner, codes.PermissionDenied},
		{hostgateway.ErrDenied, codes.PermissionDenied},
		{hostgateway.ErrInvalidRequest, codes.InvalidArgument},
		{hostgateway.ErrNotFound, codes.NotFound},
		{hostgateway.ErrActionFailed, codes.FailedPrecondition},
		{hostgateway.ErrUnavailable, codes.Unavailable},
		{errors.New("other"), codes.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			f := newHostFixture(t, true, true)
			f.gateway.OpenURLFunc = func(context.Context, hostgateway.Identity, string) error { return tc.err }
			_, err := f.handler.OpenURL(f.ctx(t), &agentv1.OpenURLRequest{Url: "https://a.example.com"})
			assertCode(t, err, tc.want)
		})
	}
}
