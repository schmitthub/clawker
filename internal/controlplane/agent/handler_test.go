package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
)

// ctxWithPeer builds a ctx the handler will see when called via gRPC
// over mTLS. Used by identity_interceptor_test.go (kept here so the
// helper survives handler test churn).
func ctxWithPeer(certRaw []byte, cn string, peerIP net.IP) context.Context {
	leaf := &x509.Certificate{
		Raw:     certRaw,
		Subject: pkix.Name{CommonName: cn},
	}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	authInfo := credentials.TLSInfo{State: state}
	addr := &net.TCPAddr{IP: peerIP, Port: 4242}
	return peer.NewContext(context.Background(), &peer.Peer{Addr: addr, AuthInfo: authInfo})
}

// inspectorFn is a small in-package fake for ContainerInspector.
type inspectorFn func(ctx context.Context, id string) (ContainerInfo, error)

func (f inspectorFn) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	return f(ctx, id)
}

// TestRegister_ShortCircuitsPermissionDenied pins the dead-code
// behavior of AgentService.Register in this branch: clawkerd does not
// call Register, the CLI is the registry writer. Any caller that
// reaches Register receives codes.PermissionDenied immediately.
// Future agent→CP RPC revival will replace the early reject with the
// real verification chain and revive the body's cross-check tests.
func TestRegister_ShortCircuitsPermissionDenied(t *testing.T) {
	slots := agentslots.NewRegistry(nil, 0, nil)
	t.Cleanup(slots.Stop)
	reg := agentregistry.NewRegistry(nil)
	inspector := inspectorFn(func(_ context.Context, _ string) (ContainerInfo, error) {
		t.Fatal("inspector must not be called — handler short-circuits before reaching it")
		return ContainerInfo{}, nil
	})
	h := NewHandler(slots, reg, inspector, nil)

	_, err := h.Register(context.Background(), &agentv1.RegisterRequest{
		AgentName:    "any",
		Project:      "any",
		CodeVerifier: "any",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- MobyInspector tests ---

// stubInspectorAPIClient is a minimal in-package fake for the moby
// APIClient surface MobyInspector touches.
type stubInspectorAPIClient struct {
	mobyclient.APIClient
	resp mobyclient.ContainerInspectResult
	err  error
}

func (s stubInspectorAPIClient) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return s.resp, s.err
}

func TestMobyInspector_Inspect_NilNetworkSettings(t *testing.T) {
	stub := stubInspectorAPIClient{
		resp: mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				Config:          &container.Config{Labels: map[string]string{"x": "y"}},
				NetworkSettings: nil,
			},
		},
	}
	insp := MobyInspector{Client: stub}

	info, err := insp.Inspect(context.Background(), "ctr-id")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errMissingNetworkSettings),
		"missing NetworkSettings must surface as the typed sentinel for handler-side branching")
	assert.Equal(t, "y", info.Labels["x"])
}

func TestMobyInspector_Inspect_MissingClawkerNetEndpoint(t *testing.T) {
	stub := stubInspectorAPIClient{
		resp: mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				Config: &container.Config{Labels: map[string]string{}},
				NetworkSettings: &container.NetworkSettings{
					Networks: map[string]*network.EndpointSettings{
						"some-other-net": {
							IPAddress: netip.MustParseAddr("172.30.0.5"),
						},
					},
				},
			},
		},
	}
	insp := MobyInspector{Client: stub}

	info, err := insp.Inspect(context.Background(), "ctr-id")
	require.NoError(t, err)
	assert.Nil(t, info.NetworkIP, "container not on clawker-net must yield nil NetworkIP")
}

func TestMobyInspector_Inspect_NormalizesIPv4(t *testing.T) {
	stub := stubInspectorAPIClient{
		resp: mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				Config: &container.Config{Labels: map[string]string{}},
				NetworkSettings: &container.NetworkSettings{
					Networks: map[string]*network.EndpointSettings{
						consts.Network: {
							IPAddress: netip.MustParseAddr("172.28.0.5"),
						},
					},
				},
			},
		},
	}
	insp := MobyInspector{Client: stub}

	info, err := insp.Inspect(context.Background(), "ctr-id")
	require.NoError(t, err)
	require.NotNil(t, info.NetworkIP)
	assert.Equal(t, 4, len(info.NetworkIP), "NetworkIP must be normalized to 4-byte IPv4 form")
	assert.True(t, info.NetworkIP.Equal(net.IPv4(172, 28, 0, 5)))
}
