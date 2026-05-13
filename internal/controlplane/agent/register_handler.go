package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"net/netip"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// inspectTimeout bounds the read-only docker inspect call inside
// Register. Decoupled from the per-RPC ctx so a CP-side cancel
// (driveRegister's RegisterDone timeout, CP shutdown) does not abort
// the inspect mid-flight and turn it into a "container not found"
// rejection.
const inspectTimeout = 5 * time.Second

// ContainerInspector is the docker-side seam the Register handler uses
// to resolve the container_id read from the cert URI SAN. Returns the
// moby InspectResponse so the handler can read labels (project +
// agent_name cross-check) and the clawker-net IP (peer-IP
// verification).
//
// Implementations: in production, the moby client (wrapped by
// pkg/whail). In tests, a struct with an Inspect closure.
type ContainerInspector interface {
	Inspect(ctx context.Context, containerID string) (mobycontainer.InspectResponse, error)
}

// mobyContainerInspector adapts a moby APIClient to ContainerInspector.
// The Register handler holds this adapter rather than the raw moby
// client to keep its interface narrow.
type mobyContainerInspector struct {
	cli mobyclient.APIClient
}

// NewMobyContainerInspector wraps a moby APIClient as a
// ContainerInspector for the Register handler.
func NewMobyContainerInspector(cli mobyclient.APIClient) ContainerInspector {
	return &mobyContainerInspector{cli: cli}
}

func (m *mobyContainerInspector) Inspect(ctx context.Context, containerID string) (mobycontainer.InspectResponse, error) {
	res, err := m.cli.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return mobycontainer.InspectResponse{}, err
	}
	return res.Container, nil
}

// Handler serves the Register RPC on the CP's clawker-net agent
// listener. It captures the live mTLS peer's cert thumbprint, reads
// the container_id from the cert URI SAN (urn:clawker:container:<id>),
// cross-checks the request's identity claims against (a) the cert's
// canonical CN, (b) the docker container's labels, and (c) the peer
// IP versus the container's clawker-net IP, then writes the row.
//
// All rejection paths return codes.PermissionDenied with a generic
// envelope. The structured log line carries the specific failure
// classification for operator triage.
type Handler struct {
	agentv1.UnimplementedAgentServiceServer
	registry  Registry
	inspector ContainerInspector
	log       *logger.Logger
	clock     func() time.Time
}

// NewHandler constructs a Register handler. registry is the CP-owned
// agentregistry; inspector resolves docker containers; log defaults
// to logger.Nop() when nil. clock defaults to time.Now.
//
// registry and inspector MUST be non-nil — either being nil is a
// wiring bug that would NPE on the first Register call. Reject at
// construction so the failure surfaces at CP startup rather than at
// the first agent boot.
func NewHandler(registry Registry, inspector ContainerInspector, log *logger.Logger) (*Handler, error) {
	if registry == nil {
		return nil, errors.New("agent.NewHandler: registry is required")
	}
	if inspector == nil {
		return nil, errors.New("agent.NewHandler: inspector is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Handler{
		registry:  registry,
		inspector: inspector,
		log:       log,
		clock:     time.Now,
	}, nil
}

// Compile-time guard: Handler must satisfy the AgentServiceServer
// interface so cmd/clawker-cp can register it on the agent gRPC
// listener. Embedding UnimplementedAgentServiceServer satisfies the
// forward-compatibility marker required by gRPC's generated code.
var _ agentv1.AgentServiceServer = (*Handler)(nil)

// Register is the one-time-per-container handshake CP triggers via a
// RegisterRequired Command on the Session bidi stream. clawkerd
// presents a CLI-CA-signed cert with a URI SAN binding it to a
// specific container_id and calls Register; CP captures the
// thumbprint at handler entry and writes the agentregistry row.
//
// Rejection paths (all return PermissionDenied, with the
// classification logged at Warn for operator triage):
//   - peer.FromContext yields no TLS auth info or no certs (would only
//     happen on a misconfigured listener)
//   - cert URI SAN missing or malformed
//   - cert CN does not equal canonical CN derived from request fields
//   - request agent_name or project malformed
//   - container_id from SAN is unknown to docker, or container labels
//     don't match the request
//   - peer IP doesn't match the container's clawker-net IP
//   - existing row for this container_id has a different thumbprint
//     (cert replay; CLI is the only legit cert source)
//
// Idempotent retry: an existing row whose thumbprint matches the
// captured thumbprint returns Welcome silently — Session retries
// after the row was already written.
func (h *Handler) Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.Welcome, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "register: nil request")
	}

	// Validate identity claims via typed constructors. Malformed
	// inputs surface here so the handler doesn't reach for the docker
	// daemon on garbage.
	project, err := auth.NewProjectSlug(req.GetProject())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "register: invalid project")
	}
	agentName, err := auth.NewAgentName(req.GetAgentName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "register: invalid agent name")
	}

	// Capture peer cert + thumbprint at handler entry. The thumbprint
	// is the load-bearing identity binding we write to the registry.
	//
	// IdentityInterceptor already pinned leaf.Subject.CommonName to
	// consts.ContainerClawkerd before reaching this handler, so we
	// don't re-check it here. The remaining identity gate is the
	// agent URI SAN: the canonical the cert carries must match the
	// canonical derived from the request's (project, agentName).
	leaf, peerIP, err := peerLeafAndIP(ctx)
	if err != nil {
		h.log.Warn().Err(err).Str("event", "agent_register_no_peer").Msg("peer cert/IP unavailable")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	thumbprint := sha256.Sum256(leaf.Raw)

	certCanonical, ok := auth.AgentCanonicalFromCert(leaf)
	if !ok {
		h.log.Warn().Str("event", "agent_register_no_agent_san").Msg("cert missing agent URI SAN")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	expectedCanonical := auth.CanonicalAgentCN(project, agentName)
	if subtle.ConstantTimeCompare([]byte(certCanonical), []byte(expectedCanonical)) != 1 {
		h.log.Warn().
			Str("event", "agent_register_canonical_mismatch").
			Str("cert_canonical", certCanonical).
			Str("expected_canonical", expectedCanonical).
			Msg("cert agent SAN does not match request identity")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}

	// Read container_id from the cert's URI SAN. Cert without that
	// SAN is malformed (CLI MintAgentCert always embeds it).
	containerID, ok := auth.ContainerIDFromCert(leaf)
	if !ok {
		h.log.Warn().Str("event", "agent_register_no_container_san").Msg("cert missing container URI SAN")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}

	// Cross-check against the docker container — labels + peer IP.
	// Inspect runs against a detached ctx with a short timeout so an
	// upstream caller cancel (driveRegister's RegisterDone timeout, CP
	// shutdown mid-handler) does not abort a read-only docker call
	// whose result we'd otherwise misclassify as "container_not_found".
	// 5s is comfortably more than docker daemon p99 inspect latency.
	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer inspectCancel()
	inspect, err := h.inspector.Inspect(inspectCtx, containerID)
	if err != nil {
		h.log.Warn().Err(err).
			Str("event", "agent_register_container_not_found").
			Str("container_id", containerID).
			Msg("docker inspect failed for container_id from cert SAN")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	if !labelsMatchRequest(inspect, project, agentName) {
		h.log.Warn().
			Str("event", "agent_register_label_mismatch").
			Str("container_id", containerID).
			Msg("container labels do not match request identity")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	if !peerIPMatchesContainer(inspect, peerIP) {
		h.log.Warn().
			Str("event", "agent_register_peer_ip_mismatch").
			Str("container_id", containerID).
			Str("peer_ip", peerIP.String()).
			Msg("peer IP does not match container clawker-net IP")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}

	// Idempotency: an existing row with the same thumbprint means CP
	// already accepted this container's cert in a prior call; the
	// Session retry that triggered another RegisterRequired needn't
	// rewrite. A row with a DIFFERENT thumbprint is a replay attempt
	// — reject.
	if existing, lookupErr := h.registry.LookupByContainerID(containerID); lookupErr == nil && existing != nil {
		if existing.Thumbprint == thumbprint {
			h.log.Info().
				Str("event", "agent_register_idempotent").
				Str("container_id", containerID).
				Msg("Register call hit existing row with matching thumbprint; returning Welcome")
			return &agentv1.Welcome{}, nil
		}
		h.log.Warn().
			Str("event", "agent_register_thumbprint_replay").
			Str("container_id", containerID).
			Msg("existing row has different thumbprint; rejecting")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	} else if lookupErr != nil && !errors.Is(lookupErr, ErrUnknownAgent) {
		h.log.Warn().Err(lookupErr).
			Str("event", "agent_register_lookup_error").
			Str("container_id", containerID).
			Msg("registry lookup failed pre-Add")
		return nil, status.Error(codes.Internal, "register: lookup failed")
	}

	// Write the row. Add validates the typed identity inputs and
	// surfaces a typed error on malformed input — but we already
	// validated above, so any error here is sqlite-side (constraint
	// violation, disk full, etc.).
	now := h.clock()
	if err := h.registry.Add(Entry{
		AgentName:    agentName.String(),
		Project:      project.String(),
		ContainerID:  containerID,
		Thumbprint:   thumbprint,
		RegisteredAt: now,
		LastSeen:     now,
	}); err != nil {
		h.log.Error().Err(err).
			Str("event", "agent_register_add_failed").
			Str("container_id", containerID).
			Msg("registry Add failed")
		return nil, status.Error(codes.Internal, "register: persist failed")
	}

	h.log.Info().
		Str("event", "agent_registered").
		Str("container_id", containerID).
		Str("agent", agentName.String()).
		Str("project", project.String()).
		Msg("agent registered (CP-driven)")
	return &agentv1.Welcome{}, nil
}

// peerLeafAndIP extracts the peer's leaf cert + remote IP from a gRPC
// context. The TLS-auth shape comes from credentials.TLSInfo (the
// standard adapter installed by grpc.Creds); the remote IP is parsed
// from peer.Addr.
func peerLeafAndIP(ctx context.Context) (*x509.Certificate, netip.Addr, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return nil, netip.Addr{}, errors.New("no peer info in context")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, netip.Addr{}, errors.New("peer is not TLS-authenticated")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, netip.Addr{}, errors.New("peer has no certificates")
	}
	leaf := tlsInfo.State.PeerCertificates[0]

	addr, err := remoteAddrToNetip(p.Addr)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	return leaf, addr, nil
}

// labelsMatchRequest checks the container's dev.clawker.agent and
// dev.clawker.project labels against the typed identity from the
// request. The labels are CLI-set at container create time (see
// container_create.go); a mismatch means either a manually-tampered
// container or a request claiming an identity the cert+container
// pairing doesn't actually carry.
func labelsMatchRequest(c mobycontainer.InspectResponse, project auth.ProjectSlug, agentName auth.AgentName) bool {
	if c.Config == nil {
		return false
	}
	labels := c.Config.Labels
	gotAgent := labels[consts.LabelAgent]
	gotProject := labels[consts.LabelProject]
	return gotAgent == agentName.String() && gotProject == project.String()
}

// peerIPMatchesContainer cross-checks the peer's remote IP against
// the container's clawker-net endpoint IP. A matching cert presented
// from a different IP is a replay attempt — reject.
func peerIPMatchesContainer(c mobycontainer.InspectResponse, peerIP netip.Addr) bool {
	if c.NetworkSettings == nil {
		return false
	}
	endpoint, ok := c.NetworkSettings.Networks[consts.Network]
	if !ok || !endpoint.IPAddress.IsValid() {
		return false
	}
	// netip.Addr from moby; equal-after-Unmap absorbs the IPv4-in-v6
	// representation difference.
	return endpoint.IPAddress.Unmap() == peerIP.Unmap()
}
