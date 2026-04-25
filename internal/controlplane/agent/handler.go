// Package agent serves the clawker.agent.v1.AgentService gRPC surface.
//
// Identity binding chain (load-bearing — see
// .serena/memories/cp-initiative-branch-4-clawkerd-auth-plan):
//
//  1. The CLI announced the agent via AdminService.AnnounceAgent. The CP
//     stored a slot keyed by canonical agent_name with the
//     CLI-asserted container_id, expected cert thumbprint, and PKCE
//     S256 challenge.
//  2. clawkerd boots inside the container, reads the bootstrap material
//     from a strict-perm path, exchanges its CLI-signed assertion for
//     a Hydra access token, and dials the CP's agent-listener with
//     mTLS using the per-agent leaf cert. Bearer token attached.
//  3. AuthInterceptor (Task 10) verifies the bearer token + per-method
//     scope (agent:self:register). mTLS itself is enforced by the
//     listener's tls.Config; the handler reads the peer cert from
//     gRPC's peer.FromContext.
//  4. Register (this package) cross-checks: PKCE consume on the slot,
//     SHA-256(peer_cert.Raw) == slot.expected_thumbprint, peer IP ==
//     Docker's clawker-net IP for slot.container_id, container labels
//     match agent_name. Every mismatch returns a single
//     codes.PermissionDenied so attackers can't tell which check
//     failed.
//  5. On success the registry is keyed by the cert thumbprint.
//     Per-agent RPCs in later branches resolve identity by hashing
//     the peer cert and looking up — there is no path for an agent to
//     claim an identity other than what its TLS cert proves.
package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	mobyclient "github.com/moby/moby/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerInspector is the narrow Docker dependency the handler needs
// at Register: a single ContainerInspect call that yields the
// clawker-net IP plus the container labels. Defining the interface
// here (instead of taking *docker.Client directly) keeps the package's
// import surface tight and gives unit tests a clean seam to forge
// adversarial Docker responses.
//
//go:generate moq -rm -pkg mocks -out mocks/inspector_mock.go . ContainerInspector
type ContainerInspector interface {
	Inspect(ctx context.Context, containerID string) (ContainerInfo, error)
}

// ContainerInfo carries exactly what Register needs from Docker. The
// network IP is the IPv4 address Docker assigned the container on
// `clawker-net`; labels are read straight from the container's
// Config.Labels map.
type ContainerInfo struct {
	NetworkIP net.IP
	Labels    map[string]string
}

// Handler implements agentv1.AgentServiceServer.
type Handler struct {
	agentv1.UnimplementedAgentServiceServer

	slots    agentslots.Registry
	registry agentregistry.Registry
	docker   ContainerInspector
	log      *logger.Logger
}

// NewHandler constructs a Handler. All dependencies are required —
// nil slots, registry, or inspector means the package consumer made a
// wiring mistake; panic loudly rather than swallow with a runtime nil
// dereference at Register time.
func NewHandler(slots agentslots.Registry, reg agentregistry.Registry, inspector ContainerInspector, log *logger.Logger) *Handler {
	if slots == nil {
		panic("agent: slot registry required")
	}
	if reg == nil {
		panic("agent: agent registry required")
	}
	if inspector == nil {
		panic("agent: container inspector required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Handler{
		slots:    slots,
		registry: reg,
		docker:   inspector,
		log:      log,
	}
}

// Register completes the PKCE handshake for one agent. Every failure
// path returns a single codes.PermissionDenied with no detail — an
// attacker probing for valid agents must not learn which check
// rejected them.
func (h *Handler) Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResult, error) {
	if req == nil || req.AgentName == "" || req.CodeVerifier == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_name and code_verifier required")
	}

	peerCert, peerIP, err := peerCertAndIP(ctx)
	if err != nil {
		h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent register: missing peer auth info")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}
	thumbprint := sha256.Sum256(peerCert.Raw)

	// (a) PKCE consume — atomic. Returned slot carries the CLI-asserted
	// container_id and expected cert thumbprint for the cross-checks
	// below.
	slot, err := h.slots.Consume(req.AgentName, req.CodeVerifier)
	if err != nil {
		h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent register: PKCE consume rejected")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (b) Cert thumbprint match — defense vs cert swap in the bootstrap
	// tmpfs between announce and clawkerd boot. Constant-time compare
	// so failure latency doesn't leak which byte differed.
	expectedThumb, err := hex.DecodeString(slot.ExpectedCertThumbprint)
	if err != nil || subtle.ConstantTimeCompare(thumbprint[:], expectedThumb) != 1 {
		h.log.Warn().Str("agent", req.AgentName).Msg("agent register: cert thumbprint mismatch")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (c) Docker cross-check: container exists, has clawker-net IP, and
	// labels declare the same canonical agent name CLI announced.
	info, err := h.docker.Inspect(ctx, slot.ContainerID)
	if err != nil {
		h.log.Warn().Err(err).Str("container_id", slot.ContainerID).Msg("agent register: docker inspect failed")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (d) Peer IP must match the container's clawker-net IP — defense
	// vs cert+verifier theft replayed from a different container.
	if info.NetworkIP == nil || !info.NetworkIP.Equal(peerIP) {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("expected_ip", ipString(info.NetworkIP)).
			Str("peer_ip", peerIP.String()).
			Msg("agent register: peer IP does not match container")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (e) Label cross-check — defense vs label tampering after announce.
	if got := info.Labels[consts.LabelAgent]; !strings.EqualFold(got, req.AgentName) {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("label", got).
			Msg("agent register: label mismatch")
		return nil, status.Error(codes.PermissionDenied, "registration rejected")
	}

	// All checks passed. Pin to registry; subsequent per-agent RPCs
	// resolve identity by recomputing SHA-256 over their TLS peer
	// cert and looking up here.
	now := nowFn()
	h.registry.Add(agentregistry.Entry{
		AgentName:    req.AgentName,
		ContainerID:  slot.ContainerID,
		Thumbprint:   thumbprint,
		RegisteredAt: now,
		LastSeen:     now,
	})

	h.log.Info().
		Str("agent", req.AgentName).
		Str("container_id", slot.ContainerID).
		Msg("agent register: registered")
	return &agentv1.RegisterResult{}, nil
}

// peerCertAndIP extracts the TLS peer cert and IPv4 from the gRPC
// context. Both come from the listener's tls.Config (server-side mTLS
// enforced, so a verified client cert is guaranteed if peer info is
// present); returns an error if either is missing so the caller can
// log + fail closed.
func peerCertAndIP(ctx context.Context) (cert *certParseResult, ip net.IP, err error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return nil, nil, fmt.Errorf("no peer info in context")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, nil, fmt.Errorf("peer is not TLS-authenticated")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, nil, fmt.Errorf("peer has no certificates")
	}
	leaf := tlsInfo.State.PeerCertificates[0]

	// p.Addr is typically a *net.TCPAddr; pull the IP, normalize to
	// IPv4 form when possible so the equality check against Docker's
	// IPv4 address doesn't trip on `::ffff:` mapped IPs.
	host, _, splitErr := net.SplitHostPort(p.Addr.String())
	if splitErr != nil {
		host = p.Addr.String()
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return nil, nil, fmt.Errorf("peer addr %q is not a valid IP", host)
	}
	if v4 := parsed.To4(); v4 != nil {
		parsed = v4
	}
	return &certParseResult{Raw: leaf.Raw}, parsed, nil
}

// certParseResult is the minimal shape Register needs from the peer
// cert — only the DER bytes flow into the SHA-256 thumbprint. Keeping
// the type tiny (vs returning *x509.Certificate) makes it obvious the
// handler doesn't trust any other cert field for identity.
type certParseResult struct{ Raw []byte }

// nowFn is a package-level seam so tests can pin RegisteredAt/LastSeen.
var nowFn = func() time.Time { return time.Now() }

// ipString returns the dotted form for non-nil IPs, "<nil>" otherwise.
// Avoids the panic path in net.IP.String when expected_ip is missing
// from a malformed Docker response.
func ipString(ip net.IP) string {
	if ip == nil {
		return "<nil>"
	}
	return ip.String()
}

// MobyInspector wraps a moby Docker client into the local
// ContainerInspector interface. Used by the wiring in
// cmd/clawker-cp/main.go so the handler has a non-leaky dependency
// on Docker types — tests can swap in a moq generated from
// ContainerInspector instead of building a fake APIClient.
type MobyInspector struct {
	Client mobyclient.APIClient
}

func (m MobyInspector) Inspect(ctx context.Context, containerID string) (ContainerInfo, error) {
	res, err := m.Client.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return ContainerInfo{}, err
	}
	c := res.Container
	info := ContainerInfo{
		Labels: c.Config.Labels,
	}
	if c.NetworkSettings == nil {
		return info, nil
	}
	if endpoint, ok := c.NetworkSettings.Networks[consts.Network]; ok && endpoint.IPAddress.IsValid() {
		info.NetworkIP = net.ParseIP(endpoint.IPAddress.String())
		if v4 := info.NetworkIP.To4(); v4 != nil {
			info.NetworkIP = v4
		}
	}
	return info, nil
}
