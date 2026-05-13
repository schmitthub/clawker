package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// IdentityOptedOutMethods returns the data-driven policy map of agent
// RPC methods that are EXEMPT from the REGISTRY-LOOKUP half of the
// identity check. The binary-CN pin (Subject.CommonName ==
// consts.ContainerClawkerd) still runs for every method on this list
// — opt-out only relaxes the thumbprint→Entry lookup, not the cert-
// subject pin.
//
// Register is exempt because the registry row keyed by the peer cert
// thumbprint does not exist yet — the entire point of the call is to
// CREATE that row. Going through the registry-lookup half would
// reject every legitimate Register call with PermissionDenied. The
// Register handler does its own per-call cross-checks (SAN-canonical
// vs request fields + container_id SAN + peer IP + container labels)
// so opt-out doesn't strip security; it relocates the registry gate
// from the interceptor to the handler.
//
// The shape mirrors AgentMethodScopes(): a build-time test walks the
// AgentService_ServiceDesc and asserts every method has either an
// explicit opt-out entry or falls into the default identity-required
// path. Adding an RPC without a deliberate policy decision fails the
// test, not the runtime — exactly the fail-secure posture the package
// aims for.
func IdentityOptedOutMethods() map[string]bool {
	return map[string]bool{
		"/" + agentv1.ServiceName + "/Register": true,
	}
}

// entryCtxKey is the unexported key under which IdentityInterceptor
// attaches the resolved *Entry. Using an unexported
// struct type guarantees no other package (and no caller code) can
// forge or accidentally collide with the registry-bound identity —
// the only path to read it back is EntryFromContext below.
type entryCtxKey struct{}

// WithEntry attaches the resolved registry entry to ctx for downstream
// handlers. Exposed so test code and future identity-augmenting
// interceptors can attach an entry without the resolved-thumbprint
// dance; production code never needs to call this directly (the
// interceptor does).
//
// Panics on a nil entry. A typed-nil pointer survives `(*Entry)(nil)`
// type assertions on the way back out of EntryFromContext as
// `(nil, true)` — a silent identity vacuum that downstream handlers
// would dereference. Mirrors agent.Add's panic-on-misuse
// posture so the wiring bug surfaces during development.
func WithEntry(ctx context.Context, entry *Entry) context.Context {
	if entry == nil {
		panic("agent: WithEntry called with nil entry")
	}
	return context.WithValue(ctx, entryCtxKey{}, entry)
}

// EntryFromContext returns the registry entry IdentityInterceptor
// attached to ctx. ok=false means the RPC is on the opt-out list (the
// handler verifies identity itself) or the interceptor was bypassed in
// a test that didn't set up identity wiring. Defensive against typed-
// nil context values: a nil entry returns ok=false even if the type
// assertion technically succeeds, so handlers can treat ok=true as
// "non-nil entry available".
func EntryFromContext(ctx context.Context) (*Entry, bool) {
	entry, ok := ctx.Value(entryCtxKey{}).(*Entry)
	return entry, ok && entry != nil
}

// IdentityInterceptor returns paired unary and stream interceptors
// that resolve mTLS peer identity to a registry entry on every non-
// opted-out method. Wired AFTER AuthInterceptor on the agent listener
// so token validation runs first and a missing-identity rejection
// never burns introspector traffic.
//
// reg is required (panic on nil — wiring regression). optedOut is a
// data-driven policy map; entries are matched on full gRPC method
// path ("/clawker.agent.v1.AgentService/Connect"). log is replaced
// with logger.Nop() if nil so the panic-recovery path of grpc-go
// never sees a nil deref inside the interceptor.
//
// Every rejection returns codes.PermissionDenied with the same generic
// envelope ("registration rejected") as Connect's other rejections —
// attackers must not learn which check failed.
func IdentityInterceptor(reg Registry, optedOut map[string]bool, log *logger.Logger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	if reg == nil {
		panic("agent: identity interceptor requires non-nil registry")
	}
	if log == nil {
		log = logger.Nop()
	}
	// nil opt-out → empty map, which means every method falls through
	// to the registry-lookup path. Worst case for a wiring regression
	// is "every method is identity-required" — fail-secure, not
	// fail-open.
	if optedOut == nil {
		optedOut = map[string]bool{}
	}
	// Validate every key against the AgentService proto descriptor so a
	// typo (e.g. "Connect" lowercased to "connect" or a method renamed
	// in proto without a matching update here) panics at startup
	// instead of silently breaking. A stale key is dangerous in two
	// ways: a typo that no longer matches any real method falls through
	// to the registry-lookup path for the (still real) method name —
	// fail-secure but locks legit callers out. The build-time test
	// TestIdentityOptedOut_NoStaleEntriesAndConnectLocked catches this
	// for IdentityOptedOutMethods() callers, but does not cover an
	// externally-constructed map; this runtime validation closes that
	// gap.
	validMethods := make(map[string]struct{}, len(agentv1.AgentService_ServiceDesc.Methods)+len(agentv1.AgentService_ServiceDesc.Streams))
	for _, m := range agentv1.AgentService_ServiceDesc.Methods {
		validMethods["/"+agentv1.ServiceName+"/"+m.MethodName] = struct{}{}
	}
	for _, s := range agentv1.AgentService_ServiceDesc.Streams {
		validMethods["/"+agentv1.ServiceName+"/"+s.StreamName] = struct{}{}
	}
	for k := range optedOut {
		if _, ok := validMethods[k]; !ok {
			panic("agent: identity interceptor opt-out has stale key: " + k)
		}
	}

	resolve := func(ctx context.Context, method string) (context.Context, error) {
		// Universal: read the peer's leaf cert and pin
		// Subject.CommonName to the deterministic clawkerd binary
		// identity. Runs BEFORE the opt-out branch so Register also
		// goes through this gate — a peer presenting a non-clawkerd
		// cert is rejected before reaching any handler.
		pid, err := peerIdentityFromContext(ctx)
		if err != nil {
			log.Warn().Err(err).Str("method", method).Msg("agent identity: missing peer auth info")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}
		if subtle.ConstantTimeCompare([]byte(pid.CommonName), []byte(consts.ContainerClawkerd)) != 1 {
			log.Warn().
				Str("method", method).
				Str("peer_cn", pid.CommonName).
				Str("expected_cn", consts.ContainerClawkerd).
				Msg("agent identity: peer CN not authorized")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		// Opt-out methods (Register) skip only the registry lookup —
		// they self-authenticate via per-handler cross-checks. The
		// CN pin above still ran.
		if optedOut[method] {
			return ctx, nil
		}

		// Reject peers whose cert is missing the agent URI SAN —
		// non-opt-out RPCs need a canonical identity to feed into
		// Registry.Lookup. Empty AgentFullName here means MintAgentCert
		// didn't run or a legacy cert is in play; either way, refuse.
		if pid.AgentFullName == "" {
			log.Warn().Str("method", method).Msg("agent identity: missing agent URI SAN")
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}

		thumbprint := sha256.Sum256(pid.Raw)
		// Pass the SAN-sourced canonical agent identity through so the
		// registry can verify it against the entry's stored canonical
		// (Project, AgentName short-form). Without this cross-check a
		// future regression that re-keys the registry by thumbprint
		// alone would silently authorize any peer presenting a
		// registered thumbprint regardless of the cert SAN —
		// defense-in-depth against thumbprint reuse.
		entry, err := reg.Lookup(thumbprint, pid.AgentFullName)
		if err != nil {
			// Differentiate the unknown-thumbprint case (operator-
			// expected: agent never registered or already evicted) from
			// any future internal error (e.g. a registry backend that
			// gains I/O). Wire response is generic PermissionDenied for
			// both — attackers must not learn which path failed — but
			// the log distinction guides the operator to the right
			// root cause.
			if errors.Is(err, ErrUnknownAgent) {
				log.Warn().Str("method", method).Msg("agent identity: thumbprint not registered")
			} else {
				log.Error().Err(err).Str("method", method).Msg("agent identity: registry lookup failed")
			}
			return nil, status.Error(codes.PermissionDenied, "registration rejected")
		}
		return WithEntry(ctx, entry), nil
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
// handler reads the original ctx without the entry attached, silently
// breaking identity binding for every streaming RPC.
//
// Note on the `ctx` field: project CLAUDE.md says "NEVER store
// context.Context in struct fields." This is the rare legitimate
// exception — gRPC's `ServerStream` interface mandates a `Context()`
// method, and wrapping the stream with a derived context is the
// only way to inject WithEntry-augmented values into streaming RPC
// handlers. Don't "fix" this field; the rule is for I/O structs
// where ctx should flow as a method parameter.
type identityServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *identityServerStream) Context() context.Context { return s.ctx }
