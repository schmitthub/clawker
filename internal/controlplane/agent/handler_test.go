package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

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
	"github.com/schmitthub/clawker/internal/auth"
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

// fixtureRegister builds the world Register expects: a slot keyed by
// the (thumbprint, agent_name) composite, an inspector that returns
// clawker-net IP + labels declaring agentName, and a peer cert whose
// CN matches agentName.
type fixtureOpts struct {
	// agentName is the short agent name (the user-typed identifier,
	// e.g. "dev"). The handler composes the canonical
	// "clawker.<project>.<agent>" CN from (project, agentName) via
	// auth.CanonicalAgentCN; tests pass the short form too unless
	// they're specifically forging an adversarial mismatch.
	agentName string
	// project is the project slug paired with agentName for the
	// composite identity. Empty string is the unscoped/2-segment
	// naming case.
	project      string
	verifier     string
	containerID  string
	certRaw      []byte
	certCN       string // override; defaults to canonical(project, agent)
	peerIP       net.IP
	dockerIP     net.IP
	labelAgent   string // override; defaults to agentName
	labelProject string // override; defaults to project
	inspectErr   error
	wrongThumb   bool
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
	if opts.labelProject == "" {
		opts.labelProject = opts.project
	}
	if opts.certCN == "" {
		opts.certCN = auth.CanonicalAgentCN(auth.MustProjectSlug(opts.project), auth.MustAgentName(opts.agentName))
	}

	now := time.Unix(100, 0)
	slots := agentslots.NewRegistry(func() time.Time { return now }, time.Hour, nil)
	t.Cleanup(slots.Stop)

	thumb := sha256.Sum256(opts.certRaw)
	if opts.wrongThumb {
		// Flip the first byte so the slot's stored thumbprint differs
		// from the one Register computes from the peer cert. With the
		// composite key, this also flips the slot's lookup key — so
		// Consume returns ErrSlotInvalid with no need for a separate
		// thumbprint compare in the handler.
		thumb[0] ^= 0xff
	}

	require.NoError(t, slots.Reserve(agentslots.Slot{
		AgentName:              opts.agentName,
		Project:                opts.project,
		ContainerID:            opts.containerID,
		ExpectedCertThumbprint: thumb,
		Challenge:              pkceChallengeForTest(opts.verifier),
		ChallengeMethod:        consts.ChallengeMethodS256,
	}))

	reg := agentregistry.NewRegistry(nil)

	inspector := inspectorFn(func(_ context.Context, _ string) (ContainerInfo, error) {
		if opts.inspectErr != nil {
			return ContainerInfo{}, opts.inspectErr
		}
		return ContainerInfo{
			NetworkIP: opts.dockerIP,
			Labels: map[string]string{
				consts.LabelAgent:   opts.labelAgent,
				consts.LabelProject: opts.labelProject,
			},
		}, nil
	})

	// Pin the handler's clock so RegisteredAt/LastSeen are deterministic.
	h := NewHandler(slots, reg, inspector, nil, WithClock(func() time.Time { return now }))
	return &fixture{handler: h, registry: reg, slots: slots, opts: opts}
}

// pkceChallengeForTest mirrors agentslots.pkceChallenge — kept local so
// the test does not depend on an unexported helper from another
// package.
func pkceChallengeForTest(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ctxWithPeer builds a ctx the handler will see when called via gRPC
// over mTLS. The leaf cert carries Subject.CommonName as the canonical
// "clawker.<project>.<agent>" so the handler's CN cross-check has a
// value to compare against.
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

// TestRegister_EmptyProject_HappyPath pins the 2-segment naming case:
// empty project is a legitimate value (matches docker.ContainerName
// behavior). The canonical CN is "clawker.<agent>", the dev.clawker.project
// label is missing on the container, and the slot Reserve / Register /
// registry Add must all accept the empty-string project consistently.
func TestRegister_EmptyProject_HappyPath(t *testing.T) {
	const verifier = "verifier"
	certRaw := []byte("cert-empty-project")
	const agentName = "solo"
	peerIP := net.IPv4(172, 28, 0, 7)

	f := newFixture(t, fixtureOpts{
		project:     "", // unscoped
		agentName:   agentName,
		verifier:    verifier,
		containerID: "ctr-empty",
		certRaw:     certRaw,
		peerIP:      peerIP,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.ProjectSlug{}, auth.MustAgentName(agentName)), peerIP)
	welcome, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: "", CodeVerifier: verifier,
	})
	require.NoError(t, err)
	require.NotNil(t, welcome)
	require.NotNil(t, welcome.Config)

	assert.Equal(t, 0, f.slots.Len(), "slot must consume on empty-project happy path")

	// Registry lookup uses canonical 2-segment CN.
	thumb := sha256.Sum256(certRaw)
	got, err := f.registry.Lookup(thumb, "clawker.solo")
	require.NoError(t, err)
	assert.Equal(t, agentName, got.AgentName)
	assert.Equal(t, "", got.Project)
}

func TestRegister_HappyPath(t *testing.T) {
	const verifier = "verifier-bytes"
	certRaw := []byte("cert-der-payload")
	const project, agentName = "alpha", "bravo"
	peerIP := net.IPv4(172, 28, 0, 5)

	f := newFixture(t, fixtureOpts{
		project:     project,
		agentName:   agentName,
		verifier:    verifier,
		containerID: "ctr-12345",
		certRaw:     certRaw,
		peerIP:      peerIP,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	welcome, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.NoError(t, err)
	require.NotNil(t, welcome)
	require.NotNil(t, welcome.Config)

	// Slot was consumed.
	assert.Equal(t, 0, f.slots.Len())

	// Registry has the new entry — Lookup uses thumbprint + canonical
	// CN, exactly the pair downstream RPCs will present.
	thumb := sha256.Sum256(certRaw)
	got, err := f.registry.Lookup(thumb, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)))
	require.NoError(t, err)
	assert.Equal(t, agentName, got.AgentName)
	assert.Equal(t, project, got.Project)
	assert.Equal(t, "ctr-12345", got.ContainerID)
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})

	cases := []*agentv1.RegisterRequest{
		nil,
		{AgentName: "", Project: "p", CodeVerifier: "v"},
		{AgentName: "x", Project: "p", CodeVerifier: ""},
	}
	for _, req := range cases {
		ctx := ctxWithPeer([]byte("c"), auth.CanonicalAgentCN(auth.MustProjectSlug("p"), auth.MustAgentName("x")), net.IPv4(1, 2, 3, 4))
		_, err := f.handler.Register(ctx, req)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestRegister_NoPeerCert(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})
	// Bare context — no peer info.
	_, err := f.handler.Register(context.Background(), &agentv1.RegisterRequest{
		AgentName: "x", Project: "p", CodeVerifier: "v",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestRegister_CNMismatch: peer cert's CommonName disagrees with the
// canonical composed from the request's (project, agent_name); reject
// before slot consume.
func TestRegister_CNMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	// Cert CN claims a different project (impostor); request body says
	// (p, x). Wire-body composes to "clawker.p.x"; cert claims
	// "clawker.evil.x" — mismatch, reject before Consume.
	ctx := ctxWithPeer(certRaw, "clawker.evil.x", peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: "x", Project: "p", CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Slot must remain — CN mismatch rejects before Consume.
	assert.Equal(t, 1, f.slots.Len())
}

// TestRegister_ProjectTamper: cert minted for (p, x) but request body
// claims project "evil". Canonical from req is "clawker.evil.x"; cert
// has "clawker.p.x" — CN equality fails.
func TestRegister_ProjectTamper(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug("p"), auth.MustAgentName("x")), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: "x", Project: "evil", CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Slot must remain — CN mismatch rejects before Consume.
	assert.Equal(t, 1, f.slots.Len())
}

func TestRegister_WrongVerifier(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: "wrong",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Wrong verifier MUST leave the slot for benign retry — agentslots
	// owns this contract; we re-assert at the handler boundary so a
	// future regression doesn't inadvertently delete on mismatch.
	assert.Equal(t, 1, f.slots.Len())
}

// TestRegister_CertSwap exercises the composite-key path: a peer cert
// whose thumbprint disagrees with the slot's stored thumbprint causes
// the (thumbprint, agent_name, project) lookup to miss entirely.
func TestRegister_CertSwap(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert-a")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP, wrongThumb: true,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_PeerIPMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw:  certRaw,
		peerIP:   net.IPv4(10, 0, 0, 5),
		dockerIP: net.IPv4(10, 0, 0, 99),
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), net.IPv4(10, 0, 0, 5))
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_LabelMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project:     project,
		agentName:   agentName,
		verifier:    verifier,
		containerID: "c",
		certRaw:     certRaw,
		peerIP:      peerIP,
		labelAgent:  "y", // tampered after announce
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestRegister_ProjectLabelMismatch covers the label cross-check on
// LabelProject. Same agent label as the slot but a tampered project
// label — the handler must catch this independently of the agent label.
func TestRegister_ProjectLabelMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "alpha", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project:      project,
		agentName:    agentName,
		verifier:     verifier,
		containerID:  "c",
		certRaw:      certRaw,
		peerIP:       peerIP,
		labelProject: "beta", // tampered project label
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestRegister_DockerInspectError(t *testing.T) {
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: "v", containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
		inspectErr: errors.New("docker daemon unreachable"),
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: "v",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestRegister_MissingNetworkSettings exercises the sentinel returned
// by MobyInspector when a container has no NetworkSettings.
func TestRegister_MissingNetworkSettings(t *testing.T) {
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: "v", containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
		inspectErr: errMissingNetworkSettings,
	})

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: "v",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestRegister_ExistingThumbprintRejected pins the NEW-only contract:
// if the registry already has a row keyed by the peer's cert thumbprint,
// Register MUST reject. Legitimate restart flows regenerate the cert
// at AnnounceAgent, producing a fresh thumbprint that misses this
// branch; reaching it means stale-verifier replay or a CLI race.
func TestRegister_ExistingThumbprintRejected(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert-existing")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	// Pre-seed the registry so the second Register call hits the
	// existing-thumbprint REJECT branch.
	thumb := sha256.Sum256(certRaw)
	require.NoError(t, f.registry.Add(agentregistry.Entry{
		AgentName:    agentName,
		Project:      project,
		ContainerID:  "c",
		Thumbprint:   thumb,
		RegisteredAt: time.Unix(100, 0),
		LastSeen:     time.Unix(100, 0),
	}))

	ctx := ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP)
	_, err := f.handler.Register(ctx, &agentv1.RegisterRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Slot must remain — REJECT runs before Consume.
	assert.Equal(t, 1, f.slots.Len())
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
