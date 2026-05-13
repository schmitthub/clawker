// Identity resolution flow for AgentService RPCs:
//
//  1. The CP's agent listener enforces mTLS at the TLS layer (server
//     cert + ClientAuth EKU + chain to the CLI CA).
//  2. AuthInterceptor verifies the bearer token + per-method scope.
//  3. IdentityInterceptor (this package) reads the peer cert via
//     peer.FromContext and runs two checks:
//     (a) UNIVERSAL — Subject.CommonName == consts.ContainerClawkerd
//     (constant-time). The cert CN is the deterministic binary
//     identity; mismatch means the peer is not presenting a
//     CLI-minted agent cert. Applies to every RPC including
//     Register (no opt-out).
//     (b) NON-OPT-OUT — computes SHA-256 thumbprint over the leaf,
//     reads canonical agent identity from the URI SAN
//     (urn:clawker:agent:<canonical>), and looks up the matching
//     registry entry via Registry.Lookup.
//  4. The resolved entry is attached to ctx via WithEntry; downstream
//     handlers read it via EntryFromContext. Opted-out RPCs (Register)
//     see ok=false from EntryFromContext and verify identity themselves.
//
// All rejections return codes.PermissionDenied with a generic envelope —
// no leak about which check failed.
//
// AgentService.Register is opt-out from the registry-lookup half only
// — the binary-CN pin still runs. The Register handler in
// register_handler.go performs its own SAN-canonical compare against
// the request fields + container_id SAN + peer-IP + label cross-checks
// before writing the row.
package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/schmitthub/clawker/internal/auth"
)

// peerIdentity is the narrow projection of a TLS peer that
// IdentityInterceptor needs:
//
//   - Raw — leaf cert DER for thumbprint computation
//   - CommonName — leaf Subject.CommonName, pinned to
//     consts.ContainerClawkerd by the interceptor
//   - AgentFullName — the canonical agent identity
//     ("clawker.<project>.<agent>") sourced from the cert's URI SAN.
//     Same content the registry stores in `canonical_cn`. Spelled
//     "FullName" — NOT "AgentName" — to keep it distinct from
//     Entry.AgentName which stores only the short form ("dev").
//
// Defining a distinct projection keeps the interceptor free of a
// direct *x509.Certificate dependency on the hot path.
type peerIdentity struct {
	Raw           []byte
	CommonName    string
	AgentFullName string
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
// SAN; an empty string here signals a malformed/legacy cert that the
// interceptor rejects with PermissionDenied.
func peerIdentityFromContext(ctx context.Context) (*peerIdentity, error) {
	leaf, err := peerLeafFromContext(ctx)
	if err != nil {
		return nil, err
	}
	agentFullName, _ := auth.AgentCanonicalFromCert(leaf) // empty if SAN missing; caller checks
	return &peerIdentity{
		Raw:           leaf.Raw,
		CommonName:    leaf.Subject.CommonName,
		AgentFullName: agentFullName,
	}, nil
}
