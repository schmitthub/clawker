// Identity resolution flow for AgentService RPCs:
//
//  1. The CP's agent listener enforces mTLS at the TLS layer (server
//     cert + ClientAuth EKU + chain to the CLI CA).
//  2. AuthInterceptor verifies the bearer token + per-method scope.
//  3. IdentityInterceptor (this package) runs a universal three-stage
//     gate on EVERY RPC including Register:
//     (a) Subject.CommonName == consts.ContainerClawkerd
//     (constant-time). The cert CN is the deterministic clawkerd
//     binary identity; mismatch means the peer is not presenting a
//     CLI-minted agent cert.
//     (b) Resolve the kernel-attested peer IP to the purpose=agent
//     container that owns it on the clawker network (via ContainerByPeerIP),
//     reading the project/agent labels as the authoritative identity
//     source — Docker is independent ground truth.
//     (c) Compose the label-derived AgentFullName and constant-time
//     compare it against the cert's urn:clawker:agent: URI SAN. The
//     cert's SAN claim is VERIFIED against the IP-grounded label
//     truth — never the basis of lookup.
//  4. The resolved (containerID, project, agentName) is attached to
//     ctx via WithResolvedContainer; downstream handlers read it via
//     ResolvedContainerFromContext to avoid re-inspecting the
//     container.
//
// All rejections return codes.PermissionDenied with a generic envelope —
// no leak about which check failed.
package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/schmitthub/clawker/internal/auth"
)

// peerIdentity is the narrow projection of a TLS peer that
// IdentityInterceptor needs:
//
//   - CommonName — leaf Subject.CommonName, pinned to
//     consts.ContainerClawkerd by the interceptor
//   - AgentFullName — the agent full-name identity
//     ("clawker.<project>.<agent>") sourced from the cert's URI SAN.
//     This is the cert's CLAIM, verified by the interceptor against
//     the label-derived agent full name; never trusted on its own.
//     Empty when the cert has no agent SAN.
//   - PeerAddr — the kernel-attested remote address of the gRPC
//     connection. The trust anchor for the peer-IP → Docker → label
//     lookup; cert claims play no part in finding the container.
//
// Defining a distinct projection keeps the interceptor free of a
// direct *x509.Certificate dependency on the hot path.
type peerIdentity struct {
	CommonName    string
	AgentFullName string
	// AgentSANErr classifies the cert's urn:clawker:agent: URI SAN:
	//   - nil                      — SAN present and valid, AgentFullName populated
	//   - auth.ErrAgentSANMissing  — cert presents no agent URI SAN
	//   - auth.ErrAgentSANMalformed — scheme present but tail empty
	// IdentityInterceptor reads this to emit distinct structured-log
	// events while keeping the wire reply a uniform PermissionDenied.
	AgentSANErr error
	PeerAddr    netip.Addr
}

var errNoPeerInfo = errors.New("agent: no peer info in context")

// peerLeafFromContext returns the raw *x509.Certificate of the gRPC
// peer attached to ctx. Returns an error when ctx carries no peer, no
// TLS auth info, or no certificates — every case is a fail-secure
// path the caller maps to PermissionDenied.
//
// Used by both peerIdentityFromContext (interceptor-side projection)
// and the Register handler (needs full *x509.Certificate to read the
// container_id SAN and Subject for cross-checks).
func peerLeafFromContext(ctx context.Context) (*x509.Certificate, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return nil, errNoPeerInfo
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, fmt.Errorf("agent: peer is not TLS-authenticated")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, fmt.Errorf("agent: peer has no certificates")
	}
	return tlsInfo.State.PeerCertificates[0], nil
}

// peerIdentityFromContext returns the interceptor's narrow peer
// projection. AgentFullName is read from the urn:clawker:agent: URI
// SAN; the interceptor rejects on empty AgentFullName with its own
// structured-log event. PeerAddr is the kernel-attested gRPC remote
// address; failure to extract it is a fail-secure reject.
func peerIdentityFromContext(ctx context.Context) (*peerIdentity, error) {
	leaf, err := peerLeafFromContext(ctx)
	if err != nil {
		return nil, err
	}
	// peer.FromContext is guaranteed non-nil here because
	// peerLeafFromContext succeeded; re-read it to access p.Addr.
	p, _ := peer.FromContext(ctx)
	addr, err := remoteAddrToNetip(p.Addr)
	if err != nil {
		return nil, err
	}
	agentFullName, sanErr := auth.AgentFullNameFromCert(leaf)
	return &peerIdentity{
		CommonName:    leaf.Subject.CommonName,
		AgentFullName: agentFullName,
		AgentSANErr:   sanErr,
		PeerAddr:      addr,
	}, nil
}

// remoteAddrToNetip converts a gRPC peer's net.Addr (always *net.TCPAddr
// for the agent listener — TCP-only) into a netip.Addr. Failure means
// the gRPC server is configured with a non-TCP transport, which would
// never happen on the agent listener; returning an error keeps the
// caller fail-secure regardless.
func remoteAddrToNetip(a net.Addr) (netip.Addr, error) {
	if a == nil {
		return netip.Addr{}, errors.New("agent: peer addr is nil")
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return netip.Addr{}, fmt.Errorf("agent: split peer host:port: %w", err)
	}
	addr, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		return netip.Addr{}, fmt.Errorf("agent: parse peer addr %q: %w", host, parseErr)
	}
	return addr, nil
}

// resolvedContainerCtxKey is the unexported key under which
// IdentityInterceptor attaches the peer-IP-resolved ResolvedContainer.
// Using an unexported struct type guarantees no other package can
// forge or accidentally collide with the resolved identity — the only
// path to read it back is ResolvedContainerFromContext below.
type resolvedContainerCtxKey struct{}

// WithResolvedContainer attaches the peer-IP-resolved container identity
// to ctx for downstream handlers. Exposed so test code and future
// identity-augmenting interceptors can attach a resolved container
// directly; production code never needs to call this (the interceptor
// does it on success).
//
// A zero-value ResolvedContainer (empty ContainerID) is silently
// dropped — ctx is returned unchanged so downstream
// ResolvedContainerFromContext sees ok=false. The CP must not panic on
// the serving path (root CLAUDE.md security contract); the read-side
// defensive ok=false is the floor against the silent-identity-vacuum
// hazard.
func WithResolvedContainer(ctx context.Context, resolved ResolvedContainer) context.Context {
	if resolved.ContainerID == "" {
		return ctx
	}
	return context.WithValue(ctx, resolvedContainerCtxKey{}, resolved)
}

// ResolvedContainerFromContext returns the peer-IP-resolved container
// identity IdentityInterceptor attached to ctx. ok=false means the
// interceptor did not run on this RPC — a wiring bug for any
// AgentService handler that consumes resolved identity. Defensive
// against a zero-value resolved container (empty ContainerID): treats
// it as ok=false so handlers can rely on ok=true meaning a non-empty
// resolved container is available.
func ResolvedContainerFromContext(ctx context.Context) (ResolvedContainer, bool) {
	resolved, ok := ctx.Value(resolvedContainerCtxKey{}).(ResolvedContainer)
	if !ok {
		return ResolvedContainer{}, false
	}
	if resolved.ContainerID == "" {
		return ResolvedContainer{}, false
	}
	return resolved, true
}
