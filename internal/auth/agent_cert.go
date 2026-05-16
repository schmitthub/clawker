package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
)

// ContainerSANScheme is the URI SAN scheme used to bind a leaf cert to
// the docker container_id it was minted for. The Register handler
// reads this SAN at handler entry and rejects any cert presenting a
// container_id that doesn't match the cert's structural binding.
//
// Example: urn:clawker:container:abc123def456...
const ContainerSANScheme = "urn:clawker:container:"

// AgentSANScheme is the URI SAN scheme that carries the AgentFullName
// ("clawker.<project>.<agent>" or "clawker.<agent>" in the unscoped
// case). It lives in a URI SAN — not Subject.CommonName — because the
// composed AgentFullName can exceed x509's 64-byte CN limit once a long
// project slug + a long agent name (or a random
// docker.GenerateRandomName output) are concatenated. The cert's
// Subject.CommonName is the deterministic consts.ContainerClawkerd
// binary-identity literal instead.
//
// CP-side gates (IdentityInterceptor, Register handler) read this SAN
// via AgentFullNameFromCert and compare it against the label-derived
// AgentFullName resolved from the peer IP's Docker container. The
// dialer's capturePeer also reads it as a diagnostic for the
// SessionConnected event payload (it does not gate trust on the
// value — that's the IdentityInterceptor's job on inbound RPCs).
//
// Example: urn:clawker:agent:clawker.myapp.dev
const AgentSANScheme = "urn:clawker:agent:"

// sanState is the three-state classification sanTailFromCert returns;
// callers map it onto the package's Err*SAN{Missing,Malformed} sentinels.
type sanState int

const (
	sanMissing sanState = iota
	sanMalformed
	sanFound
)

// Tri-state SAN sentinels. CP-side gates (IdentityInterceptor,
// Register handler) classify missing vs malformed into distinct
// structured-log events while presenting a uniform PermissionDenied
// over the wire.
var (
	ErrAgentSANMissing       = errors.New("auth: cert has no urn:clawker:agent URI SAN")
	ErrAgentSANMalformed     = errors.New("auth: urn:clawker:agent URI SAN has empty tail")
	ErrContainerSANMissing   = errors.New("auth: cert has no urn:clawker:container URI SAN")
	ErrContainerSANMalformed = errors.New("auth: urn:clawker:container URI SAN has empty tail")
)

// BuildContainerSAN composes the URI SAN for a given container_id. The
// returned *url.URL embeds in x509.Certificate.URIs.
//
// Docker container IDs are 64-char hex strings (truncated forms in CLI
// output use prefixes of the same alphabet). We enforce hex-only here
// so a malformed ID (whitespace, slashes, control chars) cannot ride
// into the cert SAN — the Register handler reads this back via
// ContainerIDFromCert and uses it to look up a docker container, so an
// unvalidated value is a producer-side bug surface.
func BuildContainerSAN(containerID string) (*url.URL, error) {
	if containerID == "" {
		return nil, fmt.Errorf("container id required")
	}
	if !isHexLower(containerID) {
		return nil, fmt.Errorf("container id must be lowercase hex; got %q", containerID)
	}
	u, err := url.Parse(ContainerSANScheme + containerID)
	if err != nil {
		return nil, fmt.Errorf("build container SAN: %w", err)
	}
	return u, nil
}

// isHexLower reports whether s contains only [0-9a-f]. Docker engine's
// canonical container ID is exactly that charset; uppercase isn't
// produced by docker so we don't accept it.
func isHexLower(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ContainerIDFromCert extracts the container_id encoded as a URI SAN
// of the form urn:clawker:container:<id>. Returns
// ErrContainerSANMissing / ErrContainerSANMalformed for the two
// reject states; mirrors AgentFullNameFromCert.
func ContainerIDFromCert(cert *x509.Certificate) (string, error) {
	tail, st := sanTailFromCert(cert, ContainerSANScheme)
	switch st {
	case sanFound:
		return tail, nil
	case sanMalformed:
		return "", ErrContainerSANMalformed
	default:
		return "", ErrContainerSANMissing
	}
}

// BuildAgentSAN composes the URI SAN that carries the AgentFullName
// ("clawker.<project>.<agent>"). Takes typed ProjectSlug + AgentName so
// the AgentFullName-form rule is enforced once by AgentFullName and the
// helper trusts its inputs.
func BuildAgentSAN(project ProjectSlug, agent AgentName) (*url.URL, error) {
	if agent.IsZero() {
		return nil, fmt.Errorf("agent name required")
	}
	fullName := AgentFullName(project, agent)
	u, err := url.Parse(AgentSANScheme + fullName)
	if err != nil {
		return nil, fmt.Errorf("build agent SAN: %w", err)
	}
	return u, nil
}

// AgentFullNameFromCert extracts the AgentFullName encoded as a URI
// SAN of the form urn:clawker:agent:<agent_full_name>. Returns three
// states the IdentityInterceptor needs to classify:
//
//   - ("name", nil)                       — SAN present and valid
//   - ("", ErrAgentSANMissing)           — no urn:clawker:agent: SAN on the cert
//   - ("", ErrAgentSANMalformed)         — scheme present but empty tail
//
// The interceptor maps both error cases to a generic PermissionDenied
// over the wire (no leak about which check failed) but emits distinct
// structured-log events so operators can tell missing-binding from
// producer-side malformation.
func AgentFullNameFromCert(cert *x509.Certificate) (string, error) {
	tail, st := sanTailFromCert(cert, AgentSANScheme)
	switch st {
	case sanFound:
		return tail, nil
	case sanMalformed:
		return "", ErrAgentSANMalformed
	default:
		return "", ErrAgentSANMissing
	}
}

// sanTailFromCert walks the cert's URI SANs and returns the tail of
// the first URI whose string-form starts with prefix along with a
// three-state classification (missing / malformed / found). Shared by
// ContainerIDFromCert + AgentFullNameFromCert; only the agent helper
// surfaces the malformed state to its caller — the container helper
// folds both reject states into a single bool.
func sanTailFromCert(cert *x509.Certificate, prefix string) (string, sanState) {
	if cert == nil {
		return "", sanMissing
	}
	for _, u := range cert.URIs {
		if u == nil {
			continue
		}
		s := u.String()
		if len(s) < len(prefix) || s[:len(prefix)] != prefix {
			continue
		}
		tail := s[len(prefix):]
		if tail == "" {
			return "", sanMalformed
		}
		return tail, sanFound
	}
	return "", sanMissing
}

// AgentFullName composes the agent identity string carried in the
// cert's urn:clawker:agent: URI SAN and reconstructed on demand from
// the registry row's (project, agent_name) columns for display.
// Three-segment for a scoped project ("clawker.<project>.<agent>"),
// two-segment for the unscoped/empty-project case ("clawker.<agent>")
// to match docker.ContainerName naming.
//
// Takes typed AgentName + ProjectSlug values so the caller can't pass
// an AgentFullName form, a dot-containing name, or arbitrary characters
// here — the constructors (NewAgentName / NewProjectSlug) enforce that
// contract once and the function trusts the values from there on.
//
// Lives in this package because it is purely a function of
// consts.NamePrefix and the (project, agent) tuple — every layer that
// needs to compose or verify the AgentFullName (cert minting,
// IdentityInterceptor cert-vs-label compare, display) reaches for this
// so the rule has a single home.
func AgentFullName(project ProjectSlug, agent AgentName) string {
	if project.IsEmpty() {
		return consts.NamePrefix + "." + agent.String()
	}
	return consts.NamePrefix + "." + project.String() + "." + agent.String()
}

// AgentCert is the co-derived material produced by MintAgentCert: the
// PEM-encoded cert, its matching key, and the SHA-256 thumbprint over
// the cert DER. The three pieces are only meaningful as a unit — pairing
// a thumbprint with a different cert breaks the cert-swap defense at
// AgentService.Connect.
//
// The String/GoString methods deliberately redact the contents so the
// struct (which carries the per-agent private key) can never leak via
// `%v`, `%+v`, `%#v`, or zerolog's interface logger. Callers that need
// the raw bytes must read the fields directly.
type AgentCert struct {
	CertPEM    []byte
	KeyPEM     []byte
	Thumbprint [sha256.Size]byte
}

// String redacts every field so AgentCert can never accidentally leak
// the per-agent private key via fmt.Sprintf("%v", cert) or zerolog.
func (AgentCert) String() string { return "AgentCert{<redacted>}" }

// GoString redacts so fmt.Sprintf("%#v", cert) (and any logger that
// uses Go-syntax representation) also does not leak KeyPEM.
func (AgentCert) GoString() string { return "AgentCert{<redacted>}" }

// MintAgentCert generates a per-agent mTLS leaf signed by the CLI CA at
// caCertPath/caKeyPath. The returned material is meant to be delivered
// to the agent container's writable layer via Docker's CopyToContainer
// API (see consts.BootstrapDir) and never persisted on the host.
//
// Subject.CommonName is the deterministic consts.ContainerClawkerd
// literal so the CN length is fixed regardless of project / agent
// inputs. The per-agent AgentFullName ("clawker.<project>.<agent>" —
// composed via AgentFullName from the typed inputs) lives in a URI
// SAN (urn:clawker:agent:<agent_full_name>) instead, so a long project
// slug or a long random docker.GenerateRandomName output can't push
// the cert past x509's 64-byte CN limit. CP-side gates read the
// AgentFullName via AgentFullNameFromCert.
//
// containerID is the docker container_id this cert is being minted for.
// MintAgentCert embeds it as a second URI SAN (urn:clawker:container:
// <id>) so the CP-side Register handler can read the binding directly
// off the peer cert at handler entry. A leaked cert presented for a
// different container_id is rejected because the SAN won't match the
// docker container the peer IP resolves to.
//
// The 24h lifetime is intentional — thumbprint pinning at registry
// lookup time makes longer-lived certs safe, but a tight ceiling caps
// the blast radius if a leaf leaks. CP captures the thumbprint at
// Register handler entry from the live mTLS peer (cert.Raw → SHA-256)
// and writes it into the agentregistry row alongside container_id;
// subsequent Sessions presenting a different cert for the same
// container_id are rejected as untrusted.
//
// Returns *AgentCert (nil on error) so a caller that ignores the error
// cannot accidentally log the redacted zero-value as a successful cert.
//
// project + agent are typed (auth.ProjectSlug, auth.AgentName) so the
// caller has gone through NewProjectSlug / NewAgentName and the
// AgentFullName-form / dot-in-name / charset checks have already run. A
// raw-string caller now produces a compile error instead of a silently-
// malformed cert subject downstream.
func MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName, containerID string) (*AgentCert, error) {
	if agent.IsZero() {
		return nil, fmt.Errorf("agent name required")
	}
	if containerID == "" {
		return nil, fmt.Errorf("container id required")
	}

	caCert, caKey, err := loadCAFrom(caCertPath, caKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	containerSAN, err := BuildContainerSAN(containerID)
	if err != nil {
		return nil, fmt.Errorf("build container SAN: %w", err)
	}
	agentSAN, err := BuildAgentSAN(project, agent)
	if err != nil {
		return nil, fmt.Errorf("build agent SAN: %w", err)
	}

	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			// Deterministic binary identity. The per-agent
			// AgentFullName ("clawker.<project>.<agent>") lives in a
			// URI SAN — keeping it out of the CN frees agent names
			// from x509's 64-byte CN limit (which used to force a
			// 24-char cap on docker.GenerateRandomName output).
			CommonName:   consts.ContainerClawkerd,
			Organization: []string{"clawker"},
		},
		NotBefore: now.Add(-5 * time.Minute),
		NotAfter:  now.Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		// Dual-purpose cert: ClientAuth so clawkerd can present it
		// when dialing CP's AgentService (mTLS to AgentPort), and
		// ServerAuth so clawkerd can present the SAME cert as its
		// server cert on the :7700 ClawkerdService listener that CP
		// dials. CP-side chain verify defaults to checking
		// ExtKeyUsageServerAuth — without ServerAuth here, every
		// CP→clawkerd dial fails with "incompatible key usage".
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		// URI SANs carry the two channel-bound identities: the
		// docker container_id (Register handler reads via
		// auth.ContainerIDFromCert) and the AgentFullName
		// (IdentityInterceptor + Register read via
		// auth.AgentFullNameFromCert).
		URIs: []*url.URL{agentSAN, containerSAN},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, fmt.Errorf("marshal leaf key: %w", err)
	}

	return &AgentCert{
		CertPEM:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		KeyPEM:     pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		Thumbprint: sha256.Sum256(certDER),
	}, nil
}

// loadCAFrom mirrors auth_material.go::loadCA but takes explicit paths
// so MintAgentCert is callable without going through the consts-driven
// resolution layer.
//
// Pair consistency is enforced: the loaded CA cert's public key must
// match the loaded CA private key. A silent mismatch would let
// MintAgentCert produce a leaf signed by key K whose issuer is a CA
// cert holding a different public key — the leaf chains to nothing,
// the CP rejects it at handshake, and the failure surfaces as an
// opaque mTLS error far from the actual misconfiguration.
func loadCAFrom(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	block, _ := pem.Decode(certData)
	if block == nil {
		return nil, nil, fmt.Errorf("CA cert: no PEM block")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	caKey, err := loadECDSAKey(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load CA key: %w", err)
	}
	caPub, ok := caCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("CA cert public key is not ECDSA")
	}
	if !caPub.Equal(&caKey.PublicKey) {
		return nil, nil, fmt.Errorf("CA cert and CA key do not form a matching pair")
	}
	return caCert, caKey, nil
}
