// Package agent serves the clawker.agent.v1.AgentService gRPC surface.
//
// Identity binding chain at Connect (load-bearing):
//
//  1. The CLI announced the agent via AdminService.AnnounceAgent. The
//     CP stored a slot keyed by the composite
//     (cert_thumbprint, agent_name, project) with the CLI-asserted
//     container_id and PKCE S256 challenge.
//  2. clawkerd boots inside the container, reads the bootstrap material
//     from a strict-perm path, exchanges its CLI-signed assertion for
//     a Hydra access token, and dials the CP's agent-listener with
//     mTLS using the per-agent leaf cert. Bearer token attached.
//  3. AuthInterceptor verifies the bearer token + per-method scope
//     (agent:self:register). mTLS itself is enforced by the listener's
//     tls.Config; the handler reads the peer cert from gRPC's
//     peer.FromContext.
//  4. Connect (this package) cross-checks, in order:
//     (a) Cert CN equals auth.CanonicalAgentCN(req.Project,
//     req.AgentName) — constant-time compare. Defends announce-
//     payload tampering between cert mint and the ConnectRequest
//     body; a tampered project OR agent on the wire produces a
//     different canonical and fails this check. Runs BEFORE slot
//     consume so a CN mismatch can't burn a legitimate slot.
//     (b) Composite slot consume on (peer_cert_thumbprint,
//     agent_name, project, code_verifier) — the (thumbprint,
//     agent_name, project) lookup folds both the thumbprint and
//     project cross-checks into the map key, eliminating any
//     separate post-Consume compare. PKCE compare is constant-time
//     inside agentslots.
//     (c) Peer IP equals Docker's clawker-net IP for
//     slot.container_id (defense vs cert+verifier replay from a
//     different container).
//     (d) Container label dev.clawker.agent equals agent_name
//     (defense vs label tampering after announce).
//     (e) Container label dev.clawker.project equals slot.Project
//     (defends label tampering on the project half — checking only
//     the agent half would let an attacker who relabeled the
//     project but kept the agent name ride a slot for the wrong
//     project).
//     Every mismatch returns codes.PermissionDenied with no detail —
//     attackers must not learn which check failed.
//  5. On success the registry is keyed by the cert thumbprint, the
//     handler sends a Welcome on the server stream (signaling auth
//     success — clawkerd deletes the single-use verifier on receipt),
//     then idles on stream.Context().Done() so the connection is the
//     agent's lifetime command channel. Eviction (container exit ->
//     dockerevents -> registry/slot subscribe) cancels the stream
//     context; clawkerd disconnect closes it from the other side.
//     Per-agent RPCs in later branches resolve identity by hashing
//     the peer cert and looking up — there is no path for an agent
//     to claim an identity other than what its TLS cert proves.
package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
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
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerInspector is the narrow Docker dependency the handler needs
// at Connect: a single ContainerInspect call that yields the
// clawker-net IP plus the container labels. Defining the interface
// here (instead of taking *docker.Client directly) keeps the package's
// import surface tight and gives unit tests a clean seam to forge
// adversarial Docker responses.
//
//go:generate moq -rm -pkg mocks -out mocks/inspector_mock.go . ContainerInspector
type ContainerInspector interface {
	Inspect(ctx context.Context, containerID string) (ContainerInfo, error)
}

// ContainerInfo carries exactly what Connect needs from Docker. The
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
	// clock is the registered-at/last-seen source. Defaults to
	// time.Now when no WithClock option is supplied; tests inject a
	// fixed-time clock so RegisteredAt and LastSeen are deterministic.
	// Stored on the Handler (not a package-level var) so concurrent
	// tests cannot stomp each other's clocks via mutation of a
	// shared global.
	clock func() time.Time
}

// HandlerOption customizes Handler construction. Functional options
// keep NewHandler ergonomic at production call sites that don't need
// any customization, while letting tests inject deterministic clocks
// without a parallel constructor.
type HandlerOption func(*Handler)

// WithClock overrides the time source used to stamp RegisteredAt /
// LastSeen on agentregistry entries. Pass a fixed-time function in
// tests; production wiring omits this option and falls back to
// time.Now.
func WithClock(now func() time.Time) HandlerOption {
	return func(h *Handler) {
		if now != nil {
			h.clock = now
		}
	}
}

// NewHandler constructs a Handler. All dependencies are required —
// nil slots, registry, or inspector means the package consumer made a
// wiring mistake; panic loudly rather than swallow with a runtime nil
// dereference at Connect time.
func NewHandler(slots agentslots.Registry, reg agentregistry.Registry, inspector ContainerInspector, log *logger.Logger, opts ...HandlerOption) *Handler {
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
	h := &Handler{
		slots:    slots,
		registry: reg,
		docker:   inspector,
		log:      log,
		clock:    time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Connect opens the agent's lifetime command channel. After all five
// identity-binding cross-checks pass, the handler sends a single
// Welcome message (signaling auth success — clawkerd deletes its
// single-use PKCE verifier on receipt) and then idles on the stream
// context until eviction or client disconnect closes it. Every
// failure path returns a single codes.PermissionDenied with no detail
// — attackers must not learn which check rejected them.
func (h *Handler) Connect(req *agentv1.ConnectRequest, stream agentv1.AgentService_ConnectServer) error {
	if req == nil || req.AgentName == "" || req.CodeVerifier == "" {
		return status.Error(codes.InvalidArgument, "agent_name and code_verifier required")
	}

	// Validate (project, agent) at the wire boundary. A buggy/malicious
	// CLI that sends a canonical-form name, a dot-containing name, or
	// arbitrary characters is rejected here — downstream code (canonical
	// CN compose, slot lookup, registry insert) trusts typed values. The
	// AnnounceAgent handler already validates Project the same way; this
	// closes the symmetric loop on the Connect side.
	project, err := auth.NewProjectSlug(req.Project)
	if err != nil {
		h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent connect: invalid project")
		return status.Error(codes.InvalidArgument, "invalid project")
	}
	agentName, err := auth.NewAgentName(req.AgentName)
	if err != nil {
		h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent connect: invalid agent name")
		return status.Error(codes.InvalidArgument, "invalid agent name")
	}

	ctx := stream.Context()
	peer, peerIP, err := peerIdentityAndIP(ctx)
	if err != nil {
		h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent connect: missing peer auth info")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}
	thumbprint := sha256.Sum256(peer.Raw)

	// (a) Cert CN cross-check — defense vs announce-payload tampering
	// between cert mint and the ConnectRequest body. auth.MintAgentCert
	// composes the CN as auth.CanonicalAgentCN(project, agent); we
	// compose the same canonical here from the validated typed values
	// and equate against the peer cert. Both halves of the wire identity
	// must match what the CLI baked into the cert, otherwise reject
	// before we touch the slot registry. Constant-time compare so
	// failure latency doesn't leak which byte differed.
	wantCN := auth.CanonicalAgentCN(project, agentName)
	if subtle.ConstantTimeCompare(
		[]byte(peer.CommonName),
		[]byte(wantCN),
	) != 1 {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("project", req.Project).
			Str("cn", peer.CommonName).
			Str("expected_cn", wantCN).
			Msg("agent connect: cert CN does not match request (project, agent)")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (b) Composite slot consume — the (thumbprint, agent_name, project)
	// lookup folds the cert-thumbprint cross-check INTO the map key, so
	// a standalone thumbprint compare is no longer necessary. Mismatch
	// / missing / expired all surface as ErrSlotInvalid; PKCE compare
	// is constant-time inside agentslots.
	slot, err := h.slots.Consume(thumbprint, req.AgentName, req.Project, req.CodeVerifier)
	if err != nil {
		h.log.Warn().Err(err).
			Str("agent", req.AgentName).
			Str("project", req.Project).
			Msg("agent connect: slot consume rejected")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (c) Docker cross-check: container exists, has clawker-net IP, and
	// labels declare the same canonical agent name CLI announced.
	info, err := h.docker.Inspect(ctx, slot.ContainerID)
	if err != nil {
		// Distinguish the common Docker-side failures so the log guides
		// operators to the actual root cause:
		//   - errMissingNetworkSettings → clawker-net contract violation
		//     (container not on the shared network at all)
		//   - context.Canceled / DeadlineExceeded → client disconnect
		//     mid-handshake (stream context was canceled while inspect
		//     was in flight); not a Docker fault, log at debug
		//   - everything else → daemon unreachable / inspect API error
		// Wire response stays the generic codes.PermissionDenied.
		switch {
		case errors.Is(err, errMissingNetworkSettings):
			h.log.Warn().
				Str("agent", req.AgentName).
				Str("container_id", slot.ContainerID).
				Msg("agent connect: container missing clawker-net network settings")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			h.log.Debug().
				Str("agent", req.AgentName).
				Str("container_id", slot.ContainerID).
				Msg("agent connect: docker inspect canceled (client disconnect)")
		default:
			h.log.Warn().Err(err).Str("container_id", slot.ContainerID).Msg("agent connect: docker inspect failed")
		}
		return status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (d) Peer IP must match the container's clawker-net IP — defense
	// vs cert+verifier theft replayed from a different container.
	if info.NetworkIP == nil || !info.NetworkIP.Equal(peerIP) {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("expected_ip", ipString(info.NetworkIP)).
			Str("peer_ip", peerIP.String()).
			Msg("agent connect: peer IP does not match container")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}

	// (e) Label cross-check — defense vs label tampering after announce.
	// Both LabelAgent AND LabelProject must agree with the slot:
	// inspecting only one half would let an attacker who relabeled the
	// project (but kept the agent name) ride a slot for the wrong
	// project. EqualFold matches Docker's case-insensitive label
	// matching elsewhere in the codebase; project labels are absent for
	// the unscoped/2-segment naming case (slot.Project == "") so accept
	// an empty label string in that branch.
	if got := info.Labels[consts.LabelAgent]; !strings.EqualFold(got, slot.AgentName) {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("label_agent", got).
			Msg("agent connect: agent label mismatch")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}
	if got := info.Labels[consts.LabelProject]; !strings.EqualFold(got, slot.Project) {
		h.log.Warn().
			Str("agent", req.AgentName).
			Str("project", req.Project).
			Str("label_project", got).
			Msg("agent connect: project label mismatch")
		return status.Error(codes.PermissionDenied, "registration rejected")
	}

	// All checks passed. Send Welcome BEFORE pinning the agent in the
	// registry: receipt of Welcome by clawkerd implies server-side
	// auth fully succeeded and authorizes deletion of the single-use
	// PKCE verifier. If Send fails (client disconnect, transport
	// reset), the registry has no orphan entry and clawkerd's retry
	// path sees clean state. B5 fills in the ClawkerdConfiguration
	// payload alongside its consumer.
	if err := stream.Send(&agentv1.Command{
		Payload: &agentv1.Command_Welcome{
			Welcome: &agentv1.Welcome{Config: &agentv1.ClawkerdConfiguration{}},
		},
	}); err != nil {
		// Send failure here is overwhelmingly "client already gone"
		// (TCP reset, container OOM-killed mid-handshake). Wrap in a
		// status.Error so the wire code matches every other error
		// path's discipline (no leaked fmt-string + no codes.Unknown).
		// Unavailable, not PermissionDenied — auth succeeded, the
		// channel was the problem.
		h.log.Warn().Err(err).
			Str("agent", req.AgentName).
			Str("container_id", slot.ContainerID).
			Msg("agent connect: send welcome failed (client likely disconnected)")
		return status.Error(codes.Unavailable, "send welcome failed")
	}

	// Pin to registry; subsequent per-agent RPCs resolve identity by
	// recomputing SHA-256 over their TLS peer cert and looking up here
	// with the cert CN as the second cross-check parameter.
	now := h.clock()
	h.registry.Add(agentregistry.Entry{
		AgentName:    slot.AgentName,
		Project:      slot.Project,
		ContainerID:  slot.ContainerID,
		Thumbprint:   thumbprint,
		RegisteredAt: now,
		LastSeen:     now,
	})

	h.log.Info().
		Str("agent", req.AgentName).
		Str("project", req.Project).
		Str("container_id", slot.ContainerID).
		Msg("agent connect: registered")

	// Idle on the stream context — wait for eviction (dockerevents
	// cancels) or client disconnect (clawkerd closes). B5+ replaces
	// this block with a select on a per-agent command queue.
	<-ctx.Done()
	return nil
}

// peerIdentity is the trusted projection of the mTLS peer cert. The
// handler MUST source identity decisions only from these fields:
//   - Raw: hashed to produce the agent thumbprint (the canonical
//     identity key in agentregistry).
//   - CommonName: cross-checked against
//     auth.CanonicalAgentCN(req.Project, req.AgentName) at Connect via
//     subtle.ConstantTimeCompare. Composite — both halves must match.
//
// Adding any future identity-bearing field here requires a corresponding
// review of the trust model; everything else on *x509.Certificate is
// intentionally inaccessible to keep "what we trust about the peer"
// expressed at compile time.
type peerIdentity struct {
	Raw        []byte
	CommonName string
}

// peerIdentityAndIP extracts the trusted identity projection and IPv4
// from the gRPC context. Both come from the listener's tls.Config
// (server-side mTLS enforced, so a verified client cert is guaranteed
// if peer info is present); returns an error if either is missing so
// the caller can log + fail closed.
func peerIdentityAndIP(ctx context.Context) (*peerIdentity, net.IP, error) {
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
	return &peerIdentity{Raw: leaf.Raw, CommonName: leaf.Subject.CommonName}, parsed, nil
}

// ipString returns the dotted form for non-nil IPs, "<nil>" otherwise.
// Avoids the panic path in net.IP.String when expected_ip is missing
// from a malformed Docker response.
func ipString(ip net.IP) string {
	if ip == nil {
		return "<nil>"
	}
	return ip.String()
}

// errMissingNetworkSettings is returned by MobyInspector.Inspect when
// the inspected container has nil NetworkSettings — this is a
// clawker-net contract violation (containers we register MUST be
// attached to the shared network so the peer-IP cross-check can fire).
// The handler swallows the sentinel and returns codes.PermissionDenied
// to the wire, but logs a more specific diagnostic.
var errMissingNetworkSettings = errors.New("agent: container has no NetworkSettings")

// MobyInspector wraps a moby Docker client into the local
// ContainerInspector interface. Used by the wiring in
// cmd/clawker-cp/main.go so the handler has a non-leaky dependency
// on Docker types — tests can swap in a moq generated from
// ContainerInspector instead of building a fake APIClient.
type MobyInspector struct {
	Client mobyclient.APIClient
	Log    *logger.Logger
}

func (m MobyInspector) Inspect(ctx context.Context, containerID string) (ContainerInfo, error) {
	res, err := m.Client.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return ContainerInfo{}, err
	}
	c := res.Container
	info := ContainerInfo{}
	if c.Config != nil {
		info.Labels = c.Config.Labels
	}
	if c.NetworkSettings == nil {
		// clawker-net contract violation — log specifically (not a
		// generic "peer IP does not match" once it bubbles up to the
		// handler) and return a sentinel so callers can branch on
		// errors.Is without parsing strings.
		log := m.Log
		if log == nil {
			log = logger.Nop()
		}
		log.Warn().
			Str("container_id", containerID).
			Msg("MobyInspector: container has no NetworkSettings (clawker-net contract violation)")
		return info, errMissingNetworkSettings
	}
	if endpoint, ok := c.NetworkSettings.Networks[consts.Network]; ok && endpoint.IPAddress.IsValid() {
		info.NetworkIP = net.ParseIP(endpoint.IPAddress.String())
		if v4 := info.NetworkIP.To4(); v4 != nil {
			info.NetworkIP = v4
		}
	}
	return info, nil
}
