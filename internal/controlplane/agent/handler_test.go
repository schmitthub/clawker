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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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

// connectStreamFake satisfies agentv1.AgentService_ConnectServer with
// the minimum surface the handler touches: Context() and Send(). The
// embedded grpc.ServerStream is intentionally nil — every other
// ServerStream method (SetHeader, SendHeader, SetTrailer, SendMsg,
// RecvMsg) panics if called, surfacing any test that drifts beyond
// what the production handler does.
//
// `sendErr` is an injectable failure for the Send-Welcome path; tests
// that exercise transport failures set it to a non-nil error and
// observe the handler's response. `welcomed` is closed on the first
// successful Send so tests can synchronize on "handler is past auth
// and idling" without polling.
type connectStreamFake struct {
	grpc.ServerStream
	ctx     context.Context
	sendErr error

	mu       sync.Mutex
	sent     []*agentv1.Command
	welcomed chan struct{}
}

func newConnectStream(ctx context.Context) *connectStreamFake {
	return &connectStreamFake{ctx: ctx, welcomed: make(chan struct{})}
}

func (s *connectStreamFake) Context() context.Context { return s.ctx }
func (s *connectStreamFake) Send(c *agentv1.Command) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.mu.Lock()
	s.sent = append(s.sent, c)
	first := len(s.sent) == 1
	s.mu.Unlock()
	if first {
		close(s.welcomed)
	}
	return nil
}
func (s *connectStreamFake) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}
func (s *connectStreamFake) sentAt(i int) *agentv1.Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[i]
}

// awaitWelcome blocks until the handler's first Send returns, or the
// 1s deadline elapses (in which case the test fails). Replaces the
// busy-wait poll loop tests previously used to detect "handler past
// auth and idling".
func (s *connectStreamFake) awaitWelcome(t *testing.T) {
	t.Helper()
	select {
	case <-s.welcomed:
	case <-time.After(time.Second):
		t.Fatal("handler did not Send Welcome within 1s")
	}
}

// fixtureRegister builds the world Connect expects: a slot keyed by
// the (thumbprint, agent_name) composite, an inspector that returns
// clawker-net IP + labels declaring agentName, and a peer cert whose
// CN matches agentName (the new B-step cross-check).
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
		// from the one Connect computes from the peer cert. With the
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

// runConnect drives the streaming handler in a goroutine. Returns the
// fake stream so callers can inspect Sent commands, plus a wait-for-
// completion func that asserts the handler returned the expected
// error (or nil for the happy path) before the deadline. Goroutine
// is needed because Connect blocks on stream.Context().Done() after
// auth succeeds.
func runConnect(t *testing.T, h *Handler, ctx context.Context, req *agentv1.ConnectRequest) (*connectStreamFake, func() error) {
	t.Helper()
	stream := newConnectStream(ctx)
	done := make(chan error, 1)
	go func() {
		done <- h.Connect(req, stream)
	}()
	wait := func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			t.Fatal("Connect did not return within 2s")
			return nil
		}
	}
	return stream, wait
}

// TestConnect_EmptyProject_HappyPath pins the 2-segment naming case:
// empty project is a legitimate value (matches docker.ContainerName
// behavior). The canonical CN is "clawker.<agent>", the dev.clawker.project
// label is missing on the container (Docker never sets a label that's
// not in ContainerLabels), and the slot Reserve / Connect / registry
// Add must all accept the empty-string project consistently. Without
// this test, a future regression that adds `if slot.Project == "" {
// reject }` would silently break the unscoped-naming branch.
func TestConnect_EmptyProject_HappyPath(t *testing.T) {
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
		// labelProject left empty too — matches an unscoped container
		// where dev.clawker.project never gets set.
	})

	ctx, cancel := context.WithCancel(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.ProjectSlug{}, auth.MustAgentName(agentName)), peerIP))
	stream, wait := runConnect(t, f.handler, ctx,
		&agentv1.ConnectRequest{AgentName: agentName, Project: "", CodeVerifier: verifier})

	stream.awaitWelcome(t)
	require.Equal(t, 1, stream.sentCount())
	assert.Equal(t, 0, f.slots.Len(), "slot must consume on empty-project happy path")

	// Registry lookup uses canonical 2-segment CN. A regression that
	// composes the canonical wrong for empty project would fail here.
	thumb := sha256.Sum256(certRaw)
	got, err := f.registry.Lookup(thumb, "clawker.solo")
	require.NoError(t, err)
	assert.Equal(t, agentName, got.AgentName)
	assert.Equal(t, "", got.Project)

	cancel()
	require.NoError(t, wait())
}

func TestConnect_HappyPath(t *testing.T) {
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

	ctx, cancel := context.WithCancel(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	stream, wait := runConnect(t, f.handler, ctx,
		&agentv1.ConnectRequest{AgentName: agentName, Project: project, CodeVerifier: verifier})

	// Welcome arrives before the handler idles on ctx.Done.
	stream.awaitWelcome(t)

	// First message MUST be Welcome (clawkerd uses this as the
	// auth-success signal that allows verifier deletion).
	require.Equal(t, 1, stream.sentCount())
	cmd := stream.sentAt(0)
	welcome, ok := cmd.Payload.(*agentv1.Command_Welcome)
	require.True(t, ok, "first Command payload must be Welcome, got %T", cmd.Payload)
	require.NotNil(t, welcome.Welcome.Config)

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

	// Cancel ctx — handler must idle on stream.Context().Done() and
	// return cleanly (the eviction-driven shutdown path); a regression
	// that returned from Connect immediately after Send would tear the
	// channel down here.
	cancel()
	require.NoError(t, wait())
}

func TestConnect_RejectsMissingFields(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})

	cases := []*agentv1.ConnectRequest{
		nil,
		{AgentName: "", Project: "p", CodeVerifier: "v"},
		{AgentName: "x", Project: "p", CodeVerifier: ""},
	}
	for _, req := range cases {
		stream := newConnectStream(ctxWithPeer([]byte("c"), auth.CanonicalAgentCN(auth.MustProjectSlug("p"), auth.MustAgentName("x")), net.IPv4(1, 2, 3, 4)))
		err := f.handler.Connect(req, stream)
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Equal(t, 0, stream.sentCount(), "rejected requests must not Send anything")
	}
}

func TestConnect_NoPeerCert(t *testing.T) {
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: "v", containerID: "c",
		certRaw: []byte("c"), peerIP: net.IPv4(1, 2, 3, 4),
	})
	// Bare context — no peer info.
	stream := newConnectStream(context.Background())
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: "x", Project: "p", CodeVerifier: "v",
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestConnect_CNMismatch is the new check Connect introduces over
// Register: if the peer cert's Subject.CommonName disagrees with the
// canonical composed from the request's (project, agent_name), reject
// before slot consume — defense vs an attacker who somehow constructed
// a valid-looking ConnectRequest body but presents a cert minted for a
// different agent.
func TestConnect_CNMismatch(t *testing.T) {
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
	stream := newConnectStream(ctxWithPeer(certRaw, "clawker.evil.x", peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: "x", Project: "p", CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, 0, stream.sentCount())
	// Slot must remain — CN mismatch rejects before Consume.
	assert.Equal(t, 1, f.slots.Len())
}

// TestConnect_ProjectTamper covers the wire-body project mismatch:
// cert was minted for (p, x) but ConnectRequest body claims project
// "evil". Canonical from req is "clawker.evil.x"; cert has
// "clawker.p.x" — CN equality fails. This is the headline reason the
// CN cross-check now reads BOTH project and agent from the request.
func TestConnect_ProjectTamper(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: "p", agentName: "x", verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug("p"), auth.MustAgentName("x")), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: "x", Project: "evil", CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Slot must remain — CN mismatch rejects before Consume.
	assert.Equal(t, 1, f.slots.Len())
}

func TestConnect_WrongVerifier(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: "wrong",
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	// Wrong verifier MUST leave the slot for benign retry — agentslots
	// owns this contract; we re-assert at the handler boundary so a
	// future regression doesn't inadvertently delete on mismatch.
	assert.Equal(t, 1, f.slots.Len())
}

// TestConnect_CertSwap exercises the composite-key path: a peer cert
// whose thumbprint disagrees with the slot's stored thumbprint causes
// the (thumbprint, agent_name, project) lookup to miss entirely. Before
// the composite refactor this was a separate post-Consume compare; now
// it's implicit in the slot lookup itself.
func TestConnect_CertSwap(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert-a")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP, wrongThumb: true,
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestConnect_PeerIPMismatch(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw:  certRaw,
		peerIP:   net.IPv4(10, 0, 0, 5),
		dockerIP: net.IPv4(10, 0, 0, 99),
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), net.IPv4(10, 0, 0, 5)))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestConnect_LabelMismatch(t *testing.T) {
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

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestConnect_ProjectLabelMismatch covers the new label cross-check on
// LabelProject. Same agent label as the slot but a tampered project
// label — the handler must catch this independently of the agent label.
// Without the project-label check, an attacker who relabeled the
// project (e.g. cross-project label spoof) could ride a slot for the
// wrong project, breaking the project-as-isolation-boundary invariant.
func TestConnect_ProjectLabelMismatch(t *testing.T) {
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

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestConnect_DockerInspectError(t *testing.T) {
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: "v", containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
		inspectErr: errors.New("docker daemon unreachable"),
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: "v",
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestConnect_SendWelcomeFails exercises the post-auth Send-failure
// path: if stream.Send returns an error (most often: client already
// disconnected by the time auth completes), the handler must surface
// codes.Unavailable on the wire (NOT bare fmt.Errorf which would land
// as codes.Unknown) and must NOT pin an orphan registry entry. The
// slot is consumed regardless — Consume's single-use semantic owns
// that contract.
func TestConnect_SendWelcomeFails(t *testing.T) {
	const verifier = "v"
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: verifier, containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	stream.sendErr = errors.New("client gone")

	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: verifier,
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err),
		"Send failure must surface as codes.Unavailable, not bare fmt.Errorf -> codes.Unknown")

	// Registry must NOT have an entry — Send happens before Add, so a
	// failed Send leaves no orphan to evict.
	thumb := sha256.Sum256(certRaw)
	_, lookupErr := f.registry.Lookup(thumb, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)))
	assert.ErrorIs(t, lookupErr, agentregistry.ErrUnknownAgent,
		"failed Send must not leave an orphan registry entry")

	// Slot was still consumed (single-use; agentslots owns the contract).
	assert.Equal(t, 0, f.slots.Len())
}

// TestConnect_MissingNetworkSettings exercises the sentinel returned
// by MobyInspector when a container has no NetworkSettings. Wire
// response is the generic codes.PermissionDenied (attackers must not
// learn which check failed) but the handler must still successfully
// branch on the sentinel — a panic or wrapped-error swallow here
// would regress the diagnostic.
func TestConnect_MissingNetworkSettings(t *testing.T) {
	certRaw := []byte("cert")
	const project, agentName = "p", "x"
	peerIP := net.IPv4(10, 0, 0, 5)
	f := newFixture(t, fixtureOpts{
		project: project, agentName: agentName, verifier: "v", containerID: "c",
		certRaw: certRaw, peerIP: peerIP,
		inspectErr: errMissingNetworkSettings,
	})

	stream := newConnectStream(ctxWithPeer(certRaw, auth.CanonicalAgentCN(auth.MustProjectSlug(project), auth.MustAgentName(agentName)), peerIP))
	err := f.handler.Connect(&agentv1.ConnectRequest{
		AgentName: agentName, Project: project, CodeVerifier: "v",
	}, stream)
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// --- MobyInspector tests ---

// stubInspectorAPIClient is a minimal in-package fake for the moby
// APIClient surface MobyInspector touches. We embed mobyclient.APIClient
// to satisfy the interface (every other method panics — caller error)
// and only override ContainerInspect. Spinning up a real Docker daemon
// is unnecessary for these branches.
type stubInspectorAPIClient struct {
	mobyclient.APIClient
	resp mobyclient.ContainerInspectResult
	err  error
}

func (s stubInspectorAPIClient) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return s.resp, s.err
}

func TestMobyInspector_Inspect_NilNetworkSettings(t *testing.T) {
	// Container has no NetworkSettings — clawker-net contract violation.
	// Inspector must surface errMissingNetworkSettings so the handler
	// can log a specific diagnostic instead of conflating with the
	// generic peer-IP-mismatch path.
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
	// Labels still flow even when NetworkSettings is missing — the
	// handler doesn't actually consume them on this branch (it
	// short-circuits on the error) but losing them silently would
	// regress the diagnostic surface for any future caller.
	assert.Equal(t, "y", info.Labels["x"])
}

func TestMobyInspector_Inspect_MissingClawkerNetEndpoint(t *testing.T) {
	// Container is on Docker but not on clawker-net. NetworkIP must
	// stay nil so the handler's peer-IP check rejects fail-closed.
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
	// Docker returns the address as a netip.Addr; MobyInspector
	// re-parses through net.ParseIP and forces To4() so the handler's
	// equality check against a peer IP doesn't trip on the 16-byte
	// IPv4-mapped-IPv6 form (`::ffff:172.28.0.5`). Pin the
	// normalization explicitly so a future refactor that drops the
	// To4() call regresses immediately.
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
