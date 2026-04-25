package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
)

// inspectorFn is a small in-package fake for ContainerInspector. The
// moq-generated mock in agent/mocks/ imports the agent package and so
// can't be used from handler_test.go without an import cycle; this
// closure-backed fake is enough for the handler's tests.
type inspectorFn func(ctx context.Context, id string) (ContainerInfo, error)

func (f inspectorFn) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	return f(ctx, id)
}

// fixtureRegister builds the world Register expects: a slot with the
// given verifier hashed into its challenge, a thumbprint matching the
// supplied "cert" bytes, and an inspector that returns clawker-net IP +
// labels declaring agentName.
type fixtureOpts struct {
	agentName   string
	verifier    string
	containerID string
	certRaw     []byte
	peerIP      net.IP
	dockerIP    net.IP
	labelAgent  string
	inspectErr  error
	wrongThumb  bool
}

type fixture struct {
	handler  *Handler
	registry agentregistry.Registry
	slots    agentslots.Registry
	opts     fixtureOpts
}

func newFixture(t *testing.T, opts fixtureOpts) *fixture {
	t.Helper()
	if opts.dockerIP == nil {
		opts.dockerIP = opts.peerIP
	}
	if opts.labelAgent == "" {
		opts.labelAgent = opts.agentName
	}

	now := time.Unix(100, 0)
	slots := agentslots.NewRegistry(func() time.Time { return now }, time.Hour, nil)
	t.Cleanup(slots.Stop)

	thumb := sha256.Sum256(opts.certRaw)
	thumbHex := hex.EncodeToString(thumb[:])
	if opts.wrongThumb {
		// Flip the first byte so the constant-time compare fails but
		// the hex still parses.
		flipped := append([]byte{}, thumb[:]...)
		flipped[0] ^= 0xff
		thumbHex = hex.EncodeToString(flipped)
	}

	require.NoError(t, slots.Reserve(agentslots.Slot{
		AgentName:              opts.agentName,
		ContainerID:            opts.containerID,
		ExpectedCertThumbprint: thumbHex,
		Challenge:              pkceChallengeForTest(opts.verifier),
		ChallengeMethod:        "S256",
		ReservedAt:             now,
		ExpiresAt:              now.Add(time.Hour),
	}))

	reg := agentregistry.NewRegistry(nil)

	inspector := inspectorFn(func(_ context.Context, _ string) (ContainerInfo, error) {
		if opts.inspectErr != nil {
			return ContainerInfo{}, opts.inspectErr
		}
		return ContainerInfo{
			NetworkIP: opts.dockerIP,
			Labels:    map[string]string{consts.LabelAgent: opts.labelAgent},
		}, nil
	})

	h := NewHandler(slots, reg, inspector, nil)
	return &fixture{handler: h, registry: reg, slots: slots, opts: opts}
}

// pkceChallengeForTest mirrors agentslots.pkceChallenge — kept local so
// the test does not depend on an unexported helper from another
// package.
func pkceChallengeForTest(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ctxWithPeer returns a ctx the handler will see when called via gRPC
// over mTLS. Builds a real TLS connection state with the cert so
// peer.FromContext + credentials.TLSInfo round-trip cleanly.
func ctxWithPeer(certRaw []byte, peerIP net.IP) context.Context {
	leaf := &x509.Certificate{Raw: certRaw}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	authInfo := credentials.TLSInfo{State: state}
	addr := &net.TCPAddr{IP: peerIP, Port: 4242}
	return peer.NewContext(context.Background(), &peer.Peer{Addr: addr, AuthInfo: authInfo})
}

func TestRegister_HappyPath(t *testing.T) {
	const verifier = "verifier-bytes"
	certRaw := []byte("cert-der-payload")
	peerIP := net.IPv4(172, 28, 0, 5)

	f := newFixture(t, fixtureOpts{
		agentName:   "clawker.alpha.bravo",
		verifier:    verifier,
		containerID: "ctr-12345",
		certRaw:     certRaw,
		peerIP:      peerIP,
	})

	resp, err := f.handler.Register(
		ctxWithPeer(certRaw, peerIP),
		&agentv1.RegisterRequest{AgentName: f.opts.agentName, CodeVerifier: verifier},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Slot was consumed.
	assert.Equal(t, 0, f.slots.Len())

	// Registry has the new entry, keyed by the thumbprint Register computed.
	thumb := sha256.Sum256(certRaw)
	got, err := f.registry.Lookup(thumb)
	require.NoError(t, err)
	assert.Equal(t, f.opts.agentName, got.AgentName)
	assert.Equal(t, "ctr-12345", got.ContainerID)
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})

	cases := []*agentv1.RegisterRequest{
		nil,
		{AgentName: "", CodeVerifier: "v"},
		{AgentName: "clawker.x", CodeVerifier: ""},
	}
	for _, req := range cases {
		_, err := f.handler.Register(ctxWithPeer([]byte("c"), net.IPv4(1, 2, 3, 4)), req)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestRegister_NoPeerCert(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})
	// Bare context — no peer info.
	_, err := f.handler.Register(context.Background(), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: "v",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_WrongVerifier(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	_, err := f.handler.Register(ctxWithPeer(certRaw, peerIP), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: "wrong",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Wrong verifier MUST leave the slot for benign retry — agentslots
	// owns this contract; we re-assert at the handler boundary so a
	// future regression doesn't inadvertently delete on mismatch.
	assert.Equal(t, 1, f.slots.Len())
}

func TestRegister_CertSwap(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert-a")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP, wrongThumb: true,
	})

	_, err := f.handler.Register(ctxWithPeer(certRaw, peerIP), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_PeerIPMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: verifier, containerID: "c",
		certRaw:  certRaw,
		peerIP:   net.IPv4(10, 0, 0, 5),
		dockerIP: net.IPv4(10, 0, 0, 99),
	})

	_, err := f.handler.Register(ctxWithPeer(certRaw, net.IPv4(10, 0, 0, 5)), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_LabelMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		agentName:   "clawker.x",
		verifier:    verifier,
		containerID: "c",
		certRaw:     certRaw,
		peerIP:      peerIP,
		labelAgent:  "clawker.y", // tampered after announce
	})

	_, err := f.handler.Register(ctxWithPeer(certRaw, peerIP), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_DockerInspectError(t *testing.T) {
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		agentName: "clawker.x", verifier: "v", containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
		inspectErr: errors.New("docker daemon unreachable"),
	})

	_, err := f.handler.Register(ctxWithPeer(certRaw, peerIP), &agentv1.RegisterRequest{
		AgentName: "clawker.x", CodeVerifier: "v",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}
