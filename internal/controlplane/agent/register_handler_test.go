package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeContainerInspector is a hand-rolled ContainerInspector for the
// Register handler tests.
type fakeContainerInspector struct {
	inspectFn func(ctx context.Context, containerID string) (mobycontainer.InspectResponse, error)
}

func (f *fakeContainerInspector) Inspect(ctx context.Context, containerID string) (mobycontainer.InspectResponse, error) {
	if f.inspectFn == nil {
		return mobycontainer.InspectResponse{}, errors.New("fakeContainerInspector: no Inspect fn wired")
	}
	return f.inspectFn(ctx, containerID)
}

// peerCtxFromCertAndIP stuffs a fake TLS peer + remote IP into ctx so
// the Register handler's peerLeafAndIP path resolves. Mirrors gRPC's
// peer.NewContext shape.
func peerCtxFromCertAndIP(t *testing.T, leaf *x509.Certificate, remoteIP string) context.Context {
	t.Helper()
	tlsInfo := credentials.TLSInfo{
		State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{leaf},
		},
	}
	addr, err := net.ResolveTCPAddr("tcp", remoteIP+":52000")
	require.NoError(t, err)
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr:     addr,
		AuthInfo: tlsInfo,
	})
}

// happyContainer returns an InspectResponse the Register handler
// accepts: labels match (project, agent) and clawker-net IP matches.
func happyContainer(containerID, project, agentName, ip string) mobycontainer.InspectResponse {
	addr := netip.MustParseAddr(ip)
	return mobycontainer.InspectResponse{
		ID: containerID,
		Config: &mobycontainer.Config{
			Labels: map[string]string{
				consts.LabelProject: project,
				consts.LabelAgent:   agentName,
			},
		},
		NetworkSettings: &mobycontainer.NetworkSettings{
			Networks: map[string]*mobynetwork.EndpointSettings{
				consts.Network: {
					IPAddress: addr,
				},
			},
		},
	}
}

// signTestLeaf produces a leaf cert chained to caCert/caKey with the
// given CN and (optionally) container_id encoded as a URI SAN.
func signTestLeaf(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn, containerID string) *x509.Certificate {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"clawker"},
		},
		NotBefore:   time.Now().Add(-time.Minute),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if containerID != "" {
		uri, err := url.Parse(auth.ContainerSANScheme + containerID)
		require.NoError(t, err)
		tmpl.URIs = []*url.URL{uri}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return leaf
}

// genTestCA creates a self-signed CA pair for test cert minting.
func genTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return caCert, key
}

// newTestHandler builds a Register handler with a mock registry, fake
// inspector, and Nop logger.
func newTestHandler(reg Registry, inspector ContainerInspector) *Handler {
	return &Handler{
		registry:  reg,
		inspector: inspector,
		log:       logger.Nop(),
		clock:     func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
}

func TestRegister_HappyPath(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-happy-path"
	const project, agentName = "myapp", "dev"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), containerID)

	var added Entry
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
		AddFunc: func(e Entry) error {
			added = e
			return nil
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, gotID string) (mobycontainer.InspectResponse, error) {
			require.Equal(t, containerID, gotID)
			return happyContainer(containerID, project, agentName, "10.20.0.5"), nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	resp, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: agentName, Project: project})
	require.NoError(t, err)
	require.NotNil(t, resp)

	wantThumbprint := sha256.Sum256(leaf.Raw)
	assert.Equal(t, wantThumbprint, added.Thumbprint)
	assert.Equal(t, containerID, added.ContainerID)
	assert.Equal(t, agentName, added.AgentName)
	assert.Equal(t, project, added.Project)
}

func TestRegister_MalformedIdentity(t *testing.T) {
	h := newTestHandler(&RegistryMock{}, &fakeContainerInspector{})
	cases := []struct {
		name string
		req  *agentv1.RegisterRequest
	}{
		{"empty agent_name", &agentv1.RegisterRequest{Project: "p"}},
		{"invalid project chars", &agentv1.RegisterRequest{AgentName: "x", Project: "BAD UPPER"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.Register(context.Background(), tc.req)
			require.Error(t, err)
			// Code is the contract; message strings drift across
			// refactors and aren't worth pinning.
			st, _ := status.FromError(err)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

func TestRegister_NoPeerInfo_PermissionDenied(t *testing.T) {
	h := newTestHandler(&RegistryMock{}, &fakeContainerInspector{})
	_, err := h.Register(context.Background(), &agentv1.RegisterRequest{AgentName: "x", Project: "p"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRegister_CNMismatch(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-cn-mismatch"
	// Cert CN reflects "actual" project; request claims "different"
	// project. Handler must reject before reaching docker.
	leaf := signTestLeaf(t, caCert, caKey, "clawker.actual.dev", containerID)

	reg := &RegistryMock{}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			t.Fatalf("Inspect must not be called when CN mismatches")
			return mobycontainer.InspectResponse{}, nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "different"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRegister_NoContainerSAN(t *testing.T) {
	caCert, caKey := genTestCA(t)
	// signTestLeaf with empty containerID skips the SAN. Handler must
	// reject because it can't bind to a container_id.
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("p"), auth.MustAgentName("dev")), "")

	h := newTestHandler(&RegistryMock{}, &fakeContainerInspector{})

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "p"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRegister_ContainerLabelMismatch(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-label-mismatch"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			// Container's labels claim a different identity than the
			// cert + request — should reject.
			return happyContainer(containerID, "different-project", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(&RegistryMock{}, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRegister_PeerIPMismatch(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-ip-mismatch"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			// Container is at 10.20.0.5; peer claims 10.20.0.99.
			return happyContainer(containerID, "myapp", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(&RegistryMock{}, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.99")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRegister_IdempotentRetry_MatchingThumbprint(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-idempotent"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)
	thumb := sha256.Sum256(leaf.Raw)

	addCalled := false
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: containerID,
				Thumbprint:  thumb,
				AgentName:   "dev",
				Project:     "myapp",
			}, nil
		},
		AddFunc: func(Entry) error {
			addCalled = true
			return nil
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			return happyContainer(containerID, "myapp", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	resp, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, addCalled, "Add must not run on idempotent retry")
}

func TestRegister_ThumbprintReplay_Rejected(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-replay"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	differentThumb := sha256.Sum256([]byte("a-different-cert"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return &Entry{
				ContainerID: containerID,
				Thumbprint:  differentThumb,
				AgentName:   "dev",
				Project:     "myapp",
			}, nil
		},
		AddFunc: func(Entry) error {
			t.Fatalf("Add must not run when thumbprint differs from existing row")
			return nil
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			return happyContainer(containerID, "myapp", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

// TestRegister_InspectError_PermissionDenied pins that a docker
// inspect failure rejects the call without falling through to Add.
// Could surface as "container truly gone between dial and Register"
// or as a transient daemon error; the handler fails closed either way.
func TestRegister_InspectError_PermissionDenied(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-inspect-err"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	addCalled := false
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
		AddFunc: func(Entry) error {
			addCalled = true
			return nil
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			return mobycontainer.InspectResponse{}, errors.New("docker daemon hiccup")
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.False(t, addCalled, "Add must not run when inspect fails")
}

// TestRegister_AddError_Internal pins that a sqlite-side Add failure
// (UNIQUE-violation race, disk full, etc.) surfaces as Internal — the
// caller already passed every identity gate, so the failure is a
// server-side persistence regression, not an identity verdict.
func TestRegister_AddError_Internal(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-add-err"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) { return nil, ErrUnknownAgent },
		AddFunc: func(Entry) error {
			return errors.New("UNIQUE constraint failed: agents.thumbprint_hex")
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			return happyContainer(containerID, "myapp", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestRegister_LookupIOError_Internal pins that a sqlite read error
// (NOT ErrUnknownAgent) surfaces as Internal rather than collapsing
// to "no row" via the existing-nil short-circuit.
func TestRegister_LookupIOError_Internal(t *testing.T) {
	caCert, caKey := genTestCA(t)
	const containerID = "ctr-lookup-err"
	leaf := signTestLeaf(t, caCert, caKey, auth.CanonicalAgentCN(auth.MustProjectSlug("myapp"), auth.MustAgentName("dev")), containerID)

	addCalled := false
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, errors.New("disk i/o error")
		},
		AddFunc: func(Entry) error {
			addCalled = true
			return nil
		},
	}
	inspector := &fakeContainerInspector{
		inspectFn: func(_ context.Context, _ string) (mobycontainer.InspectResponse, error) {
			return happyContainer(containerID, "myapp", "dev", "10.20.0.5"), nil
		},
	}
	h := newTestHandler(reg, inspector)

	ctx := peerCtxFromCertAndIP(t, leaf, "10.20.0.5")
	_, err := h.Register(ctx, &agentv1.RegisterRequest{AgentName: "dev", Project: "myapp"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.False(t, addCalled, "Add must not run when lookup fails with non-ErrUnknownAgent")
}

// TestNewHandler_RejectsNilDeps pins the constructor's nil-rejection
// contract. Wiring a Handler with a nil registry or nil inspector
// would NPE on the first call — fail at construction so the failure
// surfaces at CP startup, not at the first agent boot.
func TestNewHandler_RejectsNilDeps(t *testing.T) {
	inspector := &fakeContainerInspector{}
	reg := &RegistryMock{}

	if _, err := NewHandler(nil, inspector, logger.Nop()); err == nil {
		t.Fatal("NewHandler(nil registry, _, _) must error")
	}
	if _, err := NewHandler(reg, nil, logger.Nop()); err == nil {
		t.Fatal("NewHandler(_, nil inspector, _) must error")
	}
	h, err := NewHandler(reg, inspector, logger.Nop())
	require.NoError(t, err)
	require.NotNil(t, h)
}
