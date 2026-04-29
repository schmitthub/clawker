// Package agent serves the clawker.agent.v1.AgentService gRPC surface.
//
// The package is intentionally minimal in this branch: AgentService is
// an empty proto service (the Register handshake was retired alongside
// agentslots/AnnounceAgent). What stays is the cross-cutting
// IdentityInterceptor — a unary + stream interceptor that resolves the
// peer cert thumbprint to an agentregistry entry on every non-opted-out
// agent RPC, and a small helper (peerIdentityFromContext) used by the
// interceptor to extract the peer's leaf certificate from a gRPC
// context.
//
// Identity resolution flow (when AgentService eventually carries
// per-agent inbound RPCs):
//
//  1. The CP's agent listener enforces mTLS at the TLS layer (server
//     cert + ClientAuth EKU + chain to the CLI CA).
//  2. AuthInterceptor verifies the bearer token + per-method scope.
//  3. IdentityInterceptor (this package) reads the peer cert via
//     peer.FromContext, computes its SHA-256 thumbprint, and looks up
//     the corresponding agentregistry entry. The CN cross-check is done
//     inside agentregistry.Lookup with constant-time compare against
//     the row's pre-computed canonical CN.
//  4. The resolved entry is attached to ctx via WithEntry; downstream
//     handlers read it via EntryFromContext.
//
// All rejections return codes.PermissionDenied with a generic envelope —
// no leak about which check failed.
package agent

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// peerIdentity is the narrow projection of a TLS peer that
// IdentityInterceptor needs: the leaf cert raw bytes (for thumbprint
// computation) and its Subject CommonName (for the registry CN
// cross-check). Defining a distinct projection keeps the interceptor
// free of a direct *x509.Certificate dependency.
type peerIdentity struct {
	Raw        []byte
	CommonName string
}

// peerIdentityFromContext returns the leaf cert projection for the
// gRPC peer attached to ctx. Returns an error when ctx carries no peer,
// no TLS auth info, or no certificates — every case is a fail-secure
// path that the interceptor maps to PermissionDenied.
//
// Note: the historical signature also returned the peer's network IP
// for the agent.Register handler's container-IP cross-check; since
// Register is gone, the IP is no longer extracted here. Add it back
// (alongside the same TLS unwrap) when the next inbound agent RPC
// needs it.
var errNoPeerInfo = errors.New("agent: no peer info in context")

func peerIdentityFromContext(ctx context.Context) (*peerIdentity, error) {
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
	leaf := tlsInfo.State.PeerCertificates[0]
	return &peerIdentity{Raw: leaf.Raw, CommonName: leaf.Subject.CommonName}, nil
}

// peerIPFromContext is a future-extension stub: when the next inbound
// agent RPC needs the peer's network IP for a container-binding
// cross-check, route through this helper rather than re-implementing
// peer.FromContext + net.SplitHostPort gymnastics. Currently unused;
// kept as a documented seam.
//
//nolint:unused // future-extension stub
func peerIPFromContext(ctx context.Context) (net.IP, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return nil, errNoPeerInfo
	}
	host, _, splitErr := net.SplitHostPort(p.Addr.String())
	if splitErr != nil {
		host = p.Addr.String()
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return nil, fmt.Errorf("agent: peer addr %q is not a valid IP", host)
	}
	if v4 := parsed.To4(); v4 != nil {
		parsed = v4
	}
	return parsed, nil
}
