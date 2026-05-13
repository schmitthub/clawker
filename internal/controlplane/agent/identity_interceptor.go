package agent

import (
	"context"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// IdentityInterceptor returns paired unary and stream interceptors
// that enforce the universal identity gate documented at the top of
// handler.go. Wired AFTER AuthInterceptor on the agent listener so
// token validation runs first.
//
// peerLookup is required (panic on nil — wiring regression caught
// pre-SetReady). log defaults to logger.Nop() if nil so the
// panic-recovery path of grpc-go never sees a nil deref.
//
// Every rejection returns codes.PermissionDenied with the same
// generic envelope ("registration rejected") — attackers must not
// learn which check failed. The structured log carries the
// classification via a unique event= field per stage.
func IdentityInterceptor(peerLookup ContainerByPeerIP, log *logger.Logger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	if peerLookup == nil {
		panic("agent: identity interceptor requires non-nil peer lookup")
	}
	if log == nil {
		log = logger.Nop()
	}

	resolve := func(ctx context.Context, method string) (context.Context, error) {
		pid, err := peerIdentityFromContext(ctx)
		if err != nil {
			log.Warn().Err(err).
				Str("method", method).
				Str("event", "agent_identity_peer_auth_missing").
				Msg("agent identity: missing peer auth info")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		// Stage 1: universal CN pin.
		if subtle.ConstantTimeCompare([]byte(pid.CommonName), []byte(consts.ContainerClawkerd)) != 1 {
			log.Warn().
				Str("method", method).
				Str("event", "agent_identity_cn_mismatch").
				Str("peer_cn", pid.CommonName).
				Str("expected_cn", consts.ContainerClawkerd).
				Msg("agent identity: peer CN not authorized")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		// Stage 2a: cert must carry an agent SAN. Explicit check (rather
		// than relying on the stage-3 constant-time compare's natural
		// length-mismatch fail) gives operators a distinct event for the
		// missing-SAN case and short-circuits the Docker round-trip.
		if pid.AgentFullName == "" {
			log.Warn().
				Str("method", method).
				Str("event", "agent_identity_no_agent_san").
				Msg("agent identity: cert presents no agent URI SAN")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		// Stage 2b: peer IP → Docker → labels. Distinguishing
		// ErrInvalidAgentLabels (daemon-state corruption) from
		// ErrNoContainerForPeerIP (clean no-match) on the log surface
		// lets operators triage; the wire envelope stays uniform
		// PermissionDenied either way.
		resolved, err := peerLookup.LookupByIP(ctx, pid.PeerAddr)
		if err != nil {
			switch {
			case errors.Is(err, ErrNoContainerForPeerIP):
				log.Warn().
					Str("method", method).
					Str("event", "agent_identity_peer_lookup_no_match").
					Str("peer_ip", pid.PeerAddr.String()).
					Msg("agent identity: no purpose=agent container owns peer IP")
			case errors.Is(err, ErrInvalidAgentLabels):
				log.Warn().
					Str("method", method).
					Str("event", "agent_identity_invalid_labels").
					Str("peer_ip", pid.PeerAddr.String()).
					Msg("agent identity: matched container carries invalid identity labels")
			default:
				log.Error().Err(err).
					Str("method", method).
					Str("event", "agent_identity_peer_lookup_error").
					Str("peer_ip", pid.PeerAddr.String()).
					Msg("agent identity: peer lookup failed")
			}
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		// Stage 3: cert SAN AgentFullName vs label-derived AgentFullName.
		// ResolvedContainer.Project/.AgentName are typed and pre-validated
		// by the resolver — re-running auth.NewProjectSlug here would be
		// redundant.
		labelFullName := auth.AgentFullName(resolved.Project, resolved.AgentName)
		if subtle.ConstantTimeCompare([]byte(pid.AgentFullName), []byte(labelFullName)) != 1 {
			log.Warn().
				Str("method", method).
				Str("event", "agent_identity_san_label_mismatch").
				Str("peer_ip", pid.PeerAddr.String()).
				Str("cert_agent_full_name", pid.AgentFullName).
				Str("expected_agent_full_name", labelFullName).
				Msg("agent identity: cert SAN does not match label-derived AgentFullName")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		return WithResolvedContainer(ctx, resolved), nil
	}

	unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		newCtx, err := resolve(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(newCtx, req)
	}

	stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		newCtx, err := resolve(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &identityServerStream{ServerStream: ss, ctx: newCtx})
	}

	return unary, stream
}

// identityServerStream wraps a grpc.ServerStream so the handler sees
// the identity-augmented context. CRITICAL: the Context() method MUST
// be defined on this wrapper, NOT promoted from the embedded
// ServerStream — otherwise the embedded type's Context() wins and the
// handler reads the original ctx without the resolved container
// attached, silently breaking identity binding for every streaming RPC.
//
// Note on the `ctx` field: project CLAUDE.md says "NEVER store
// context.Context in struct fields." This is the rare legitimate
// exception — gRPC's `ServerStream` interface mandates a `Context()`
// method, and wrapping the stream with a derived context is the only
// way to inject WithResolvedContainer-augmented values into streaming
// RPC handlers. Don't "fix" this field; the rule is for I/O structs
// where ctx should flow as a method parameter.
type identityServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *identityServerStream) Context() context.Context { return s.ctx }
