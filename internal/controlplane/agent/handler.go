// Identity resolution flow for non-opt-out AgentService RPCs:
//
//  1. The CP's agent listener enforces mTLS at the TLS layer (server
//     cert + ClientAuth EKU + chain to the CLI CA).
//  2. AuthInterceptor verifies the bearer token + per-method scope.
//  3. IdentityInterceptor (this package) reads the peer cert via
//     peer.FromContext, computes its SHA-256 thumbprint, and looks up
//     the corresponding registry entry via Registry.Lookup, which
//     does a constant-time CN compare against the row's pre-computed
//     canonical CN.
//  4. The resolved entry is attached to ctx via WithEntry; downstream
//     handlers read it via EntryFromContext.
//
// All rejections return codes.PermissionDenied with a generic envelope —
// no leak about which check failed.
//
// AgentService.Register is opt-out (registry row doesn't exist
// pre-call) — the Register handler in register_handler.go does its
// own peer cert + IP + label cross-checks before writing the row.
package agent

import (
	"context"
	"errors"
	"fmt"

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

var errNoPeerInfo = errors.New("agent: no peer info in context")

// peerIdentityFromContext returns the leaf cert projection for the
// gRPC peer attached to ctx. Returns an error when ctx carries no peer,
// no TLS auth info, or no certificates — every case is a fail-secure
// path that the interceptor maps to PermissionDenied.
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
