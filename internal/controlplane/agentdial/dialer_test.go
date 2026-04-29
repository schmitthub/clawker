package agentdial

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	regmocks "github.com/schmitthub/clawker/internal/controlplane/agentregistry/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// genCA produces a self-signed CA cert + key suitable for use as a
// root in x509.VerifyOptions. validity bounds the resulting cert's
// NotBefore/NotAfter so callers can test expiry-aware paths.
func genCA(t *testing.T, cn string, validity time.Duration) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

// signLeaf issues a leaf cert for cn signed by parent (the CA). The
// returned DER bytes are what would arrive in raw rawCerts during a
// TLS handshake.
func signLeaf(t *testing.T, cn string, notAfter time.Time, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) ([]byte, *x509.Certificate) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &leafKey.PublicKey, parentKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return der, cert
}

// --- capturePeerProvenance: cert chain + CN + thumbprint capture ----
// Permissive — every path returns without aborting. Failure outcomes
// surface as Reason text + ChainVerified=false; populated fields
// (PeerCN, PeerThumbprint) appear on the event payload.

func TestCapturePeerProvenance_ValidChain(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, leaf := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	var prov Provenance
	d.capturePeerProvenance([][]byte{leafDER}, &prov)

	assert.True(t, prov.ChainVerified, "trusted-CA chain must verify")
	assert.Equal(t, leaf.Subject.CommonName, prov.PeerCN)
	want := sha256.Sum256(leafDER)
	assert.Equal(t, want[:], prov.PeerThumbprint)
	assert.Empty(t, prov.Reason)
}

func TestCapturePeerProvenance_UntrustedRoot_DoesNotAbort(t *testing.T) {
	wrongCA, wrongKey := genCA(t, "wrong-ca", 24*time.Hour)
	leafDER, leaf := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), wrongCA, wrongKey)

	trustedCA, _ := genCA(t, "trusted-ca", 24*time.Hour)
	pool := x509.NewCertPool()
	pool.AddCert(trustedCA)
	d := &Dialer{caPool: pool}

	var prov Provenance
	d.capturePeerProvenance([][]byte{leafDER}, &prov)

	assert.False(t, prov.ChainVerified, "untrusted root must yield ChainVerified=false")
	// Cert-level fields still populated — subscribers need them
	// regardless of chain trust.
	assert.Equal(t, leaf.Subject.CommonName, prov.PeerCN)
	want := sha256.Sum256(leafDER)
	assert.Equal(t, want[:], prov.PeerThumbprint)
	assert.Contains(t, prov.Reason, "chain verify")
}

func TestCapturePeerProvenance_ExpiredLeaf_DoesNotAbort(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, _ := signLeaf(t, "clawker.proj.dev", time.Now().Add(-time.Minute), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	var prov Provenance
	d.capturePeerProvenance([][]byte{leafDER}, &prov)

	assert.False(t, prov.ChainVerified, "expired leaf must yield ChainVerified=false")
	assert.Equal(t, "clawker.proj.dev", prov.PeerCN)
	assert.NotEmpty(t, prov.PeerThumbprint)
	assert.Contains(t, prov.Reason, "chain verify")
}

func TestCapturePeerProvenance_NoCerts_SetsReason(t *testing.T) {
	d := &Dialer{caPool: x509.NewCertPool()}

	var prov Provenance
	d.capturePeerProvenance(nil, &prov)

	assert.False(t, prov.ChainVerified)
	assert.Empty(t, prov.PeerCN)
	assert.Empty(t, prov.PeerThumbprint)
	assert.Equal(t, "peer presented no certs", prov.Reason)
}

func TestCapturePeerProvenance_BadCertBytes_SetsReason(t *testing.T) {
	d := &Dialer{caPool: x509.NewCertPool()}

	var prov Provenance
	d.capturePeerProvenance([][]byte{[]byte("not a cert")}, &prov)

	assert.False(t, prov.ChainVerified)
	assert.Empty(t, prov.PeerCN)
	assert.Empty(t, prov.PeerThumbprint)
	assert.Contains(t, prov.Reason, "leaf parse failed")
}

// --- fillRegistryProvenance: registry-row cross-check ---------------
// Populates RegistryMatch / Miss / ThumbprintMismatch / CNMismatch
// based on agentregistry.LookupByContainerID.

func TestFillRegistryProvenance_RegistryMatch(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
	expectedCN := auth.CanonicalAgentCN(auth.MustProjectSlug("myproj"), auth.MustAgentName("dev"))
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return &agentregistry.Entry{
				AgentName:   "dev",
				Project:     "myproj",
				ContainerID: id,
				Thumbprint:  thumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	prov := Provenance{
		PeerCN:         expectedCN,
		PeerThumbprint: thumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-1", "myproj", "dev")

	assert.True(t, prov.RegistryMatch)
	assert.False(t, prov.RegistryMiss)
	assert.False(t, prov.ThumbprintMismatch)
	assert.False(t, prov.CNMismatch)
	assert.True(t, prov.CNPinMatch)
}

func TestFillRegistryProvenance_RegistryMiss(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return nil, agentregistry.ErrUnknownAgent
		},
	}
	d := &Dialer{agents: reg}

	thumb := sha256.Sum256([]byte("peer"))
	prov := Provenance{
		PeerCN:         "clawker.x.y",
		PeerThumbprint: thumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-2", "x", "y")

	assert.True(t, prov.RegistryMiss)
	assert.False(t, prov.RegistryMatch)
}

func TestFillRegistryProvenance_ThumbprintMismatch(t *testing.T) {
	peerThumb := sha256.Sum256([]byte("peer"))
	rowThumb := sha256.Sum256([]byte("registry"))
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return &agentregistry.Entry{
				AgentName:   "dev",
				Project:     "myproj",
				ContainerID: id,
				Thumbprint:  rowThumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	prov := Provenance{
		PeerCN:         "clawker.myproj.dev",
		PeerThumbprint: peerThumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-3", "myproj", "dev")

	assert.True(t, prov.ThumbprintMismatch)
	assert.False(t, prov.RegistryMatch)
	assert.False(t, prov.RegistryMiss)
	assert.False(t, prov.CNMismatch)
}

func TestFillRegistryProvenance_CNMismatch(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return &agentregistry.Entry{
				AgentName:   "dev",
				Project:     "actual",
				ContainerID: id,
				Thumbprint:  thumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	prov := Provenance{
		PeerCN:         "clawker.different.dev", // does not match clawker.actual.dev
		PeerThumbprint: thumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-4", "actual", "dev")

	assert.True(t, prov.CNMismatch)
	assert.False(t, prov.RegistryMatch)
	assert.False(t, prov.ThumbprintMismatch)
}

func TestFillRegistryProvenance_LookupErrorSetsReason(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return nil, errors.New("disk i/o failed")
		},
	}
	d := &Dialer{agents: reg}

	thumb := sha256.Sum256([]byte("peer"))
	prov := Provenance{
		PeerCN:         "clawker.x.y",
		PeerThumbprint: thumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-5", "x", "y")

	assert.False(t, prov.RegistryMatch)
	assert.False(t, prov.RegistryMiss)
	assert.Contains(t, prov.Reason, "registry lookup error")
}

func TestFillRegistryProvenance_NilRegistrySetsReason(t *testing.T) {
	d := &Dialer{agents: nil}

	thumb := sha256.Sum256([]byte("peer"))
	prov := Provenance{
		PeerCN:         "clawker.x.y",
		PeerThumbprint: thumb[:],
	}
	d.fillRegistryProvenance(&prov, "ctr-6", "x", "y")

	assert.Equal(t, "registry not wired", prov.Reason)
	assert.False(t, prov.RegistryMatch)
}

// --- computeCNPinMatch: cert-vs-labels CN derivation ----------------

func TestComputeCNPinMatch_Match(t *testing.T) {
	expected := auth.CanonicalAgentCN(auth.MustProjectSlug("foo"), auth.MustAgentName("bar"))
	assert.True(t, computeCNPinMatch(expected, "foo", "bar"))
}

func TestComputeCNPinMatch_EmptyProjectIsValid(t *testing.T) {
	expected := auth.CanonicalAgentCN(auth.MustProjectSlug(""), auth.MustAgentName("solo"))
	assert.True(t, computeCNPinMatch(expected, "", "solo"))
}

func TestComputeCNPinMatch_PeerCNDiffers(t *testing.T) {
	assert.False(t, computeCNPinMatch("clawker.other.bar", "foo", "bar"))
}

func TestComputeCNPinMatch_EmptyPeerCN(t *testing.T) {
	assert.False(t, computeCNPinMatch("", "foo", "bar"))
}

func TestComputeCNPinMatch_EmptyAgentName(t *testing.T) {
	assert.False(t, computeCNPinMatch("clawker.foo.bar", "foo", ""))
}

func TestComputeCNPinMatch_MalformedAgent(t *testing.T) {
	// dot in agent name fails NewAgentName validation.
	assert.False(t, computeCNPinMatch("clawker.foo.bad.name", "foo", "bad.name"))
}

// --- closeAndCheckLeak: FD-leak guard kept from prior test set -----

type fakeCloser struct {
	errs  []error
	calls int
}

func (f *fakeCloser) Close() error {
	defer func() { f.calls++ }()
	if f.calls < len(f.errs) {
		return f.errs[f.calls]
	}
	return nil
}

func TestCloseAndCheckLeak_BailsAfterCeiling(t *testing.T) {
	errs := make([]error, closeErrCeiling)
	for i := range errs {
		errs[i] = errors.New("transport already shut down")
	}
	c := &fakeCloser{errs: errs}
	count := 0
	d := &Dialer{}

	for i := 1; i <= closeErrCeiling; i++ {
		bail := d.closeAndCheckLeak(c, &count, logger.Nop())
		if i < closeErrCeiling {
			assert.False(t, bail, "must not bail before ceiling (iter %d)", i)
		} else {
			assert.True(t, bail, "must bail at ceiling (iter %d)", i)
		}
	}
	assert.Equal(t, closeErrCeiling, count)
	assert.Equal(t, closeErrCeiling, c.calls)
}

func TestCloseAndCheckLeak_SuccessResetsCounter(t *testing.T) {
	c := &fakeCloser{errs: []error{
		errors.New("hiccup"),
		errors.New("hiccup"),
		nil,
		errors.New("hiccup"),
	}}
	count := 0
	d := &Dialer{}

	require.False(t, d.closeAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 1, count)
	require.False(t, d.closeAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 2, count)
	// Successful close — counter resets.
	require.False(t, d.closeAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 0, count)
	// Subsequent failure starts from 1, not from 3.
	require.False(t, d.closeAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 1, count)
}

func TestCloseAndCheckLeak_LogsCloseFailure(t *testing.T) {
	c := &fakeCloser{errs: []error{errors.New("transport already shut down")}}
	count := 0
	var buf bytes.Buffer
	d := &Dialer{}

	d.closeAndCheckLeak(c, &count, logger.NewWriter(&buf))

	got := buf.String()
	assert.Contains(t, got, "agentdial_conn_close_failed")
	assert.Contains(t, got, `"close_err_count":1`)
}
