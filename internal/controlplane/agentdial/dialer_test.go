package agentdial

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	regmocks "github.com/schmitthub/clawker/internal/controlplane/agentregistry/mocks"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	slotmocks "github.com/schmitthub/clawker/internal/controlplane/agentslots/mocks"
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

func TestVerifyChainOnly_AcceptsValidChain(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, _ := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	require.NoError(t, d.verifyChainOnly([][]byte{leafDER}, nil))
}

func TestVerifyChainOnly_RejectsUntrustedRoot(t *testing.T) {
	// Leaf signed by an unrelated CA; verifier's pool contains a
	// different CA — chain build must fail.
	wrongCA, wrongKey := genCA(t, "wrong-ca", 24*time.Hour)
	leafDER, _ := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), wrongCA, wrongKey)

	trustedCA, _ := genCA(t, "trusted-ca", 24*time.Hour)
	pool := x509.NewCertPool()
	pool.AddCert(trustedCA)
	d := &Dialer{caPool: pool}

	err := d.verifyChainOnly([][]byte{leafDER}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chain verify")
}

func TestVerifyChainOnly_RejectsExpiredLeaf(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	// Leaf NotAfter already in the past.
	leafDER, _ := signLeaf(t, "clawker.proj.dev", time.Now().Add(-time.Minute), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	err := d.verifyChainOnly([][]byte{leafDER}, nil)
	require.Error(t, err)
	// x509 surfaces an "expired" string — exact phrasing is
	// stdlib-version-sensitive, so just confirm verification ran and
	// rejected the leaf.
	assert.Contains(t, err.Error(), "agentdial: chain verify")
}

func TestVerifyChainOnly_RejectsEmptyCerts(t *testing.T) {
	d := &Dialer{caPool: x509.NewCertPool()}
	err := d.verifyChainOnly(nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no certs")
}

// TestRecordRegistryProvenance_HappyPath: registry row exists and
// thumbprint+CN match. Expected log: provenance=registry_match at
// info level.
func TestRecordRegistryProvenance_HappyPath(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
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
	var buf bytes.Buffer
	d := &Dialer{agents: reg}
	d.recordRegistryProvenance("ctr-1", thumb, "clawker.myproj.dev", logger.NewWriter(&buf))

	require.Len(t, reg.LookupByContainerIDCalls(), 1)
	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "info", got["level"])
	assert.Equal(t, "registry_match", got["provenance"])
	assert.Equal(t, "clawker.myproj.dev", got["peer_cn"])
}

// TestRecordRegistryProvenance_ThumbprintMismatch: registry row
// exists but the stored thumbprint diverges from the peer's. Cert
// substitution / container-id reuse path. Expected: warn level,
// provenance=registry_thumbprint_mismatch.
func TestRecordRegistryProvenance_ThumbprintMismatch(t *testing.T) {
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
	var buf bytes.Buffer
	d := &Dialer{agents: reg}
	d.recordRegistryProvenance("ctr-2", peerThumb, "clawker.myproj.dev", logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "warn", got["level"])
	assert.Equal(t, "registry_thumbprint_mismatch", got["provenance"])
	assert.Equal(t, hex.EncodeToString(peerThumb[:]), got["peer_thumbprint"])
	assert.Equal(t, hex.EncodeToString(rowThumb[:]), got["registry_thumbprint"])
}

// TestRecordRegistryProvenance_RegistryMiss: no row for this
// containerID. Untracked container path. Expected: warn level,
// provenance=registry_miss.
func TestRecordRegistryProvenance_RegistryMiss(t *testing.T) {
	reg := &regmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agentregistry.Entry, error) {
			return nil, agentregistry.ErrUnknownAgent
		},
	}
	var buf bytes.Buffer
	d := &Dialer{agents: reg}
	d.recordRegistryProvenance("ctr-3", sha256.Sum256([]byte("peer")), "clawker.x.y", logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "warn", got["level"])
	assert.Equal(t, "registry_miss", got["provenance"])
}

// TestRecordRegistryProvenance_NilRegistry: no Registry wired (the
// dialer was constructed without one). Must short-circuit silently —
// log is empty, no panic.
func TestRecordRegistryProvenance_NilRegistry(t *testing.T) {
	var buf bytes.Buffer
	d := &Dialer{agents: nil}
	d.recordRegistryProvenance("ctr-4", sha256.Sum256([]byte("peer")), "cn", logger.NewWriter(&buf))
	assert.Equal(t, 0, buf.Len(), "nil registry must produce no log output")
}

// TestConsumeAnnounceSlot_Consumed: slot existed; Consume returned it.
// CLI-attested-start path. Expected: info, provenance=announce_slot_consumed.
func TestConsumeAnnounceSlot_Consumed(t *testing.T) {
	reservedAt := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Second)
	slots := &slotmocks.RegistryMock{
		ConsumeFunc: func(id string) (*agentslots.Slot, error) {
			return &agentslots.Slot{ReservedAt: reservedAt}, nil
		},
	}
	var buf bytes.Buffer
	d := &Dialer{slots: slots}
	d.consumeAnnounceSlot("ctr-1", logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "info", got["level"])
	assert.Equal(t, "announce_slot_consumed", got["provenance"])
}

// TestConsumeAnnounceSlot_Missing: ErrSlotInvalid is the documented
// "no slot or expired" path. Raw `docker start` case. Expected:
// info, provenance=announce_slot_missing — NOT an error.
func TestConsumeAnnounceSlot_Missing(t *testing.T) {
	slots := &slotmocks.RegistryMock{
		ConsumeFunc: func(id string) (*agentslots.Slot, error) {
			return nil, agentslots.ErrSlotInvalid
		},
	}
	var buf bytes.Buffer
	d := &Dialer{slots: slots}
	d.consumeAnnounceSlot("ctr-2", logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "info", got["level"])
	assert.Equal(t, "announce_slot_missing", got["provenance"])
}

// TestConsumeAnnounceSlot_RegistryRegression: any non-ErrSlotInvalid
// err is a Registry contract regression. Per S15 the level is
// promoted from Warn to Error so the bug surfaces in rotated logs
// instead of being filed under "noisy debug-output".
func TestConsumeAnnounceSlot_RegistryRegression(t *testing.T) {
	slots := &slotmocks.RegistryMock{
		ConsumeFunc: func(id string) (*agentslots.Slot, error) {
			return nil, errors.New("disk full: db closed")
		},
	}
	var buf bytes.Buffer
	d := &Dialer{slots: slots}
	d.consumeAnnounceSlot("ctr-3", logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "error", got["level"], "non-ErrSlotInvalid must be Error level (S15)")
	assert.Contains(t, got["message"], "Registry contract regression")
}

func TestConsumeAnnounceSlot_NilRegistry(t *testing.T) {
	var buf bytes.Buffer
	d := &Dialer{slots: nil}
	d.consumeAnnounceSlot("ctr-4", logger.NewWriter(&buf))
	assert.Equal(t, 0, buf.Len(), "nil slot registry must produce no log output")
}

// fakeCloser drives closeAndCheckLeak through a controlled error
// sequence. Returned errs are walked in order; once exhausted Close
// returns nil. Records the call count for assertions.
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

// TestCloseAndCheckLeak_BailsAfterCeiling: closeErrCeiling consecutive
// failures must bail. The function reports bail=true on the
// ceilingth failure and the counter equals the ceiling.
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

// TestCloseAndCheckLeak_SuccessResetsCounter: a successful Close
// resets the counter so a transient hiccup does not poison the
// ledger across the lifetime of the cycle.
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

// TestCloseAndCheckLeak_LogsCloseFailure: each failed Close emits
// an Error-level log line tagged with the close-error count and the
// ceiling so an operator can correlate against the fd-leak-ceiling
// SessionFailed event.
func TestCloseAndCheckLeak_LogsCloseFailure(t *testing.T) {
	c := &fakeCloser{errs: []error{errors.New("transport already shut down")}}
	count := 0
	var buf bytes.Buffer
	d := &Dialer{}

	d.closeAndCheckLeak(c, &count, logger.NewWriter(&buf))

	got := decodeOnly(t, buf.Bytes())
	assert.Equal(t, "error", got["level"])
	assert.Equal(t, "agentdial_conn_close_failed", got["event"])
	// counts are encoded as JSON numbers (float64 after decode).
	assert.Equal(t, float64(1), got["close_err_count"])
	assert.Equal(t, float64(closeErrCeiling), got["close_err_ceiling"])
}

// decodeOnly returns the FIRST decoded log line. The functions under
// test in this file emit exactly one line per call — anything else
// is a regression worth surfacing.
func decodeOnly(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	require.NotEmpty(t, bytes.TrimSpace(raw), "expected at least one log line")
	dec := json.NewDecoder(bytes.NewReader(raw))
	var line map[string]any
	require.NoError(t, dec.Decode(&line))
	// Drain the decoder once more to confirm there's no second line —
	// the production code is documented to emit exactly one.
	if dec.More() {
		var extra map[string]any
		_ = dec.Decode(&extra)
		t.Fatalf("expected exactly one log line, got at least 2; second: %v", extra)
	}
	return line
}

// Sanity guard: the message-level fields the assertions rely on are
// stable across zerolog versions (level, message, event). If zerolog
// renames any of them this test fails first instead of the assertions
// scattered through the file.
func TestZerologFieldKeysStable(t *testing.T) {
	var buf bytes.Buffer
	logger.NewWriter(&buf).Info().Str("event", "probe").Msg("hi")
	got := decodeOnly(t, buf.Bytes())
	for _, k := range []string{"level", "message", "event"} {
		_, ok := got[k]
		require.Truef(t, ok, "zerolog field %q missing — assertions assume it; raw=%s", k, strings.TrimSpace(buf.String()))
	}
}
