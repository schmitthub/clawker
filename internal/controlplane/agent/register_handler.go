package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/logger"
)

// Handler serves the Register RPC on the CP's clawker-net agent
// listener and is the SOLE writer of the agentregistry sqlite DB.
// IdentityInterceptor has already grounded the peer in the
// daemon-attested container identity (peer IP → purpose=agent
// container → labels → cross-checked cert SAN AgentFullName) and
// attached the resolved (containerID, project, agentName) to ctx. The
// handler reads that, captures the cert thumbprint at the gate that
// persists it, cross-checks the cert's container_id SAN + request
// fields against the resolved truth, and writes the registry row
// using label-derived (authoritative) values.
//
// Trust ordering: daemon labels > cert claim > request claim.
// Persisting `resolved.*` keeps the registry aligned with the daemon
// view; the request body is treated as a client claim that must agree
// but never as the source of truth.
//
// All identity-rejection paths return codes.PermissionDenied with a
// generic envelope; the structured log line carries the specific
// failure classification for operator triage. A missing resolved
// container in ctx is a wiring bug (interceptor not chained) and
// surfaces as codes.Internal.
type Handler struct {
	agentv1.UnimplementedAgentServiceServer
	registry Registry
	log      *logger.Logger
	clock    func() time.Time
}

// NewHandler constructs a Register handler. registry MUST be non-nil
// (NPE on first Register otherwise — fail at construction so the
// regression surfaces at CP startup, not at the first agent boot).
// log defaults to logger.Nop() when nil. clock defaults to time.Now.
func NewHandler(registry Registry, log *logger.Logger) (*Handler, error) {
	if registry == nil {
		return nil, errors.New("agent.NewHandler: registry is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Handler{
		registry: registry,
		log:      log,
		clock:    time.Now,
	}, nil
}

// Compile-time guard: Handler must satisfy the AgentServiceServer
// interface so cmd/clawker-cp can register it on the agent gRPC
// listener. Embedding UnimplementedAgentServiceServer satisfies the
// forward-compatibility marker required by gRPC's generated code.
var _ agentv1.AgentServiceServer = (*Handler)(nil)

// Register is the one-time-per-container handshake CP triggers via a
// RegisterRequired Command on the Session bidi stream. clawkerd
// presents a CLI-CA-signed cert and calls Register; the universal
// IdentityInterceptor has already pinned CN=ContainerClawkerd, resolved
// the peer IP to a purpose=agent container, and verified that the
// cert's urn:clawker:agent: SAN matches the label-derived
// AgentFullName. The handler captures the live mTLS peer's cert
// thumbprint, cross-checks the cert's urn:clawker:container: SAN and
// the request fields against the middleware-resolved identity, then
// writes the agentregistry row using the label-derived (authoritative)
// values.
//
// Rejection paths (all return PermissionDenied unless noted; the
// classification is logged at Warn for operator triage):
//   - resolved container missing from ctx — middleware did not run on
//     this RPC (wiring regression). Returns Internal.
//   - peer cert missing from ctx post-resolve — defense-in-depth; the
//     interceptor pre-validates the cert, so reaching this branch
//     means ctx was tampered between interceptor and handler.
//   - request agent_name or project malformed (InvalidArgument)
//   - request fields disagree with middleware-resolved labels (the
//     client lying about its own identity in the RPC body, even
//     though cert+labels agree)
//   - cert urn:clawker:container: SAN missing or doesn't match the
//     middleware-resolved container_id
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

	// Validate the client's identity claims via typed constructors so
	// the rest of the handler can compare typed values, not raw strings.
	// Ordering note: request validation runs ahead of the ctx checks so
	// a malformed request returns InvalidArgument rather than getting
	// classified as a wiring bug (Internal) on a misconfigured caller.
	project, err := auth.NewProjectSlug(req.GetProject())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "register: invalid project")
	}
	agentName, err := auth.NewAgentName(req.GetAgentName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "register: invalid agent name")
	}

	// IdentityInterceptor MUST have resolved the peer to a container
	// before reaching this handler. Absence is a wiring regression
	// (interceptor not chained on this RPC), not an identity verdict —
	// surface as Internal so an operator can distinguish "rejected"
	// from "broken".
	resolved, ok := ResolvedContainerFromContext(ctx)
	if !ok {
		h.log.Error().
			Str("event", "agent_register_no_resolved_container").
			Msg("middleware did not attach ResolvedContainer to ctx — wiring bug")
		return nil, status.Error(codes.Internal, "register: identity not resolved")
	}

	// Capture peer cert + thumbprint at handler entry. The thumbprint
	// is the registry's UNIQUE key and load-bearing identity binding —
	// deriving it from the live peer cert (rather than surfacing it
	// via ctx) keeps the gate that persists the thumbprint and the
	// gate that produced it co-located, so a future interceptor change
	// can't silently substitute the value the registry stores.
	leaf, err := peerLeafFromContext(ctx)
	if err != nil {
		h.log.Warn().Err(err).
			Str("event", "agent_register_peer_cert_missing_post_resolve").
			Msg("ctx carried resolved container but no peer cert — defensive reject")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	thumbprint := sha256.Sum256(leaf.Raw)

	// Request fields must agree with the daemon-resolved labels. The
	// middleware already confirmed cert↔labels alignment; this stage
	// catches a clawkerd whose cert and labels agree but whose RPC
	// body claims a different identity (defense-in-depth against a
	// client bug, not a malicious peer — the cert+CN pin already
	// excludes that). Plain string compare: project/agent are
	// daemon-attested labels, not secrets, and the timing channel
	// reveals nothing useful.
	if project.String() != resolved.Project.String() || agentName.String() != resolved.AgentName.String() {
		h.log.Warn().
			Str("event", "agent_register_request_label_mismatch").
			Str("request_project", project.String()).
			Str("request_agent", agentName.String()).
			Str("resolved_project", resolved.Project.String()).
			Str("resolved_agent", resolved.AgentName.String()).
			Msg("request fields do not match middleware-resolved identity")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}

	// Cert URI SAN container_id is a claim; resolved.ContainerID is
	// daemon ground truth via peer-IP. The middleware verified the
	// urn:clawker:agent: SAN but not the urn:clawker:container: SAN
	// (cert claims about the container are this handler's
	// responsibility). CLI MintAgentCert always embeds the SAN —
	// absence is a malformed cert.
	certContainerID, ok := auth.ContainerIDFromCert(leaf)
	if !ok {
		h.log.Warn().
			Str("event", "agent_register_no_container_san").
			Msg("cert missing container URI SAN")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}
	if certContainerID != resolved.ContainerID {
		h.log.Warn().
			Str("event", "agent_register_container_id_mismatch").
			Str("cert_container_id", certContainerID).
			Str("resolved_container_id", resolved.ContainerID).
			Msg("cert container SAN does not match peer-IP-resolved container_id")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	}

	// Idempotency: an existing row with the same thumbprint means CP
	// already accepted this container's cert in a prior call; the
	// Session retry that triggered another RegisterRequired needn't
	// rewrite. A row with a DIFFERENT thumbprint is a replay attempt —
	// reject. A row whose stored identity columns are malformed
	// (ErrMalformedEntry from scanEntry) is treated as unusable: evict
	// it so the Add below re-writes using the middleware-resolved
	// (and freshly validated) identity. The eviction is idempotent;
	// the subsequent Add is the normal write path.
	existing, lookupErr := h.registry.LookupByContainerID(resolved.ContainerID)
	switch {
	case lookupErr == nil && existing != nil:
		if existing.Thumbprint == thumbprint {
			h.log.Info().
				Str("event", "agent_register_idempotent").
				Str("container_id", resolved.ContainerID).
				Msg("Register call hit existing row with matching thumbprint; returning Welcome")
			return &agentv1.Welcome{}, nil
		}
		h.log.Warn().
			Str("event", "agent_register_thumbprint_replay").
			Str("container_id", resolved.ContainerID).
			Msg("existing row has different thumbprint; rejecting")
		return nil, status.Error(codes.PermissionDenied, "register: identity check failed")
	case errors.Is(lookupErr, ErrMalformedEntry):
		// Row exists but its persisted identity tuple is unreadable.
		// Evict + re-write rather than refusing the Register: the
		// middleware just verified the live cert against daemon labels,
		// so the row we're about to write is more trustworthy than the
		// malformed legacy one. Failing the evict is fatal — Add would
		// hit the same row.
		h.log.Warn().Err(lookupErr).
			Str("event", "agent_register_malformed_row_evicted").
			Str("container_id", resolved.ContainerID).
			Msg("registry row malformed; evicting before re-write")
		if evictErr := h.registry.EvictByContainerID(resolved.ContainerID); evictErr != nil {
			h.log.Error().Err(evictErr).
				Str("event", "agent_register_malformed_row_evict_failed").
				Str("container_id", resolved.ContainerID).
				Msg("evict of malformed row failed; cannot proceed with Add")
			return nil, status.Error(codes.Internal, "register: evict malformed row failed")
		}
	case lookupErr != nil && !errors.Is(lookupErr, ErrUnknownAgent):
		h.log.Warn().Err(lookupErr).
			Str("event", "agent_register_lookup_error").
			Str("container_id", resolved.ContainerID).
			Msg("registry lookup failed pre-Add")
		return nil, status.Error(codes.Internal, "register: lookup failed")
	}

	// Write the row using the label-derived identity (authoritative
	// source). Request fields were validated and confirmed to agree
	// above; persisting `resolved.*` ensures the registry stores the
	// daemon's view, not a client claim.
	now := h.clock()
	if err := h.registry.Add(Entry{
		AgentName:    resolved.AgentName,
		Project:      resolved.Project,
		ContainerID:  resolved.ContainerID,
		Thumbprint:   thumbprint,
		RegisteredAt: now,
		LastSeen:     now,
	}); err != nil {
		h.log.Error().Err(err).
			Str("event", "agent_register_add_failed").
			Str("container_id", resolved.ContainerID).
			Msg("registry Add failed")
		return nil, status.Error(codes.Internal, "register: persist failed")
	}

	h.log.Info().
		Str("event", "agent_registered").
		Str("container_id", resolved.ContainerID).
		Str("agent", resolved.AgentName.String()).
		Str("project", resolved.Project.String()).
		Msg("agent registered (CP-driven)")
	return &agentv1.Welcome{}, nil
}
