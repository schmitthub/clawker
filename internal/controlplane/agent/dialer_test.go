package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

func mustEncodeKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func mustEncodeCert(_ *testing.T, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

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

func TestCapturePeer_ValidChain(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, leaf := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	var peer peerInfo
	d.capturePeer([][]byte{leafDER}, &peer)

	assert.True(t, peer.ChainVerified, "trusted-CA chain must verify")
	assert.Equal(t, leaf.Subject.CommonName, peer.PeerCN)
	want := sha256.Sum256(leafDER)
	assert.Equal(t, want, peer.PeerThumbprint)
	assert.Empty(t, peer.CaptureReason)
}

func TestCapturePeer_UntrustedRoot_DoesNotAbort(t *testing.T) {
	wrongCA, wrongKey := genCA(t, "wrong-ca", 24*time.Hour)
	leafDER, leaf := signLeaf(t, "clawker.proj.dev", time.Now().Add(time.Hour), wrongCA, wrongKey)

	trustedCA, _ := genCA(t, "trusted-ca", 24*time.Hour)
	pool := x509.NewCertPool()
	pool.AddCert(trustedCA)
	d := &Dialer{caPool: pool}

	var peer peerInfo
	d.capturePeer([][]byte{leafDER}, &peer)

	assert.False(t, peer.ChainVerified, "untrusted root must yield ChainVerified=false")
	assert.Equal(t, leaf.Subject.CommonName, peer.PeerCN)
	want := sha256.Sum256(leafDER)
	assert.Equal(t, want, peer.PeerThumbprint)
	assert.Contains(t, peer.CaptureReason, "chain verify")
}

func TestCapturePeer_ExpiredLeaf_DoesNotAbort(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, _ := signLeaf(t, "clawker.proj.dev", time.Now().Add(-time.Minute), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &Dialer{caPool: pool}

	var peer peerInfo
	d.capturePeer([][]byte{leafDER}, &peer)

	assert.False(t, peer.ChainVerified, "expired leaf must yield ChainVerified=false")
	assert.Equal(t, "clawker.proj.dev", peer.PeerCN)
	assert.NotEqual(t, [sha256.Size]byte{}, peer.PeerThumbprint)
	assert.Contains(t, peer.CaptureReason, "chain verify")
}

func TestCapturePeer_NoCerts_SetsReason(t *testing.T) {
	d := &Dialer{caPool: x509.NewCertPool()}

	var peer peerInfo
	d.capturePeer(nil, &peer)

	assert.False(t, peer.ChainVerified)
	assert.Empty(t, peer.PeerCN)
	assert.Equal(t, [sha256.Size]byte{}, peer.PeerThumbprint)
	assert.Equal(t, "peer presented no certs", peer.CaptureReason)
}

func TestCapturePeer_BadCertBytes_SetsReason(t *testing.T) {
	d := &Dialer{caPool: x509.NewCertPool()}

	var peer peerInfo
	d.capturePeer([][]byte{[]byte("not a cert")}, &peer)

	assert.False(t, peer.ChainVerified)
	assert.Empty(t, peer.PeerCN)
	assert.Equal(t, [sha256.Size]byte{}, peer.PeerThumbprint)
	assert.Contains(t, peer.CaptureReason, "leaf parse failed")
}

// --- classifyRegistry: registry-row cross-check ---------------------

func TestClassifyRegistry_Match(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
	expectedCN := auth.CanonicalAgentCN(auth.MustProjectSlug("myproj"), auth.MustAgentName("dev"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(id string) (*Entry, error) {
			return &Entry{
				AgentName:   "dev",
				Project:     "myproj",
				ContainerID: id,
				Thumbprint:  thumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	outcome, _ := d.classifyRegistry(peerInfo{PeerCN: expectedCN, PeerThumbprint: thumb}, "ctr-1")
	assert.Equal(t, outcomeRegistryMatch, outcome)
}

func TestClassifyRegistry_Miss(t *testing.T) {
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(id string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	d := &Dialer{agents: reg}

	thumb := sha256.Sum256([]byte("peer"))
	outcome, _ := d.classifyRegistry(peerInfo{PeerCN: "clawker.x.y", PeerThumbprint: thumb}, "ctr-2")
	assert.Equal(t, outcomeRegistryMiss, outcome)
}

func TestClassifyRegistry_ThumbprintMismatch(t *testing.T) {
	peerThumb := sha256.Sum256([]byte("peer"))
	rowThumb := sha256.Sum256([]byte("registry"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(id string) (*Entry, error) {
			return &Entry{
				AgentName:   "dev",
				Project:     "myproj",
				ContainerID: id,
				Thumbprint:  rowThumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	outcome, _ := d.classifyRegistry(peerInfo{PeerCN: "clawker.myproj.dev", PeerThumbprint: peerThumb}, "ctr-3")
	assert.Equal(t, outcomeRegistryThumbprintMismatch, outcome)
}

func TestClassifyRegistry_CNMismatch(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer"))
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(id string) (*Entry, error) {
			return &Entry{
				AgentName:   "dev",
				Project:     "actual",
				ContainerID: id,
				Thumbprint:  thumb,
			}, nil
		},
	}
	d := &Dialer{agents: reg}

	outcome, _ := d.classifyRegistry(peerInfo{PeerCN: "clawker.different.dev", PeerThumbprint: thumb}, "ctr-4")
	assert.Equal(t, outcomeRegistryCNMismatch, outcome)
}

func TestClassifyRegistry_LookupErrorReturnsNotQueried(t *testing.T) {
	reg := &RegistryMock{
		LookupByContainerIDFunc: func(id string) (*Entry, error) {
			return nil, errors.New("disk i/o failed")
		},
	}
	d := &Dialer{agents: reg}

	outcome, detail := d.classifyRegistry(peerInfo{PeerCN: "clawker.x.y", PeerThumbprint: sha256.Sum256([]byte("p"))}, "ctr-5")
	assert.Equal(t, outcomeRegistryNotQueried, outcome)
	assert.Contains(t, detail, "registry lookup error")
}

func TestClassifyRegistry_NilRegistryReturnsNotQueried(t *testing.T) {
	d := &Dialer{agents: nil}

	outcome, detail := d.classifyRegistry(peerInfo{PeerCN: "clawker.x.y", PeerThumbprint: sha256.Sum256([]byte("p"))}, "ctr-6")
	assert.Equal(t, outcomeRegistryNotQueried, outcome)
	assert.Equal(t, "registry not wired", detail)
}

// --- computeCNPinMatch: cert-vs-labels CN derivation ----------------

func TestComputeCNPinMatch(t *testing.T) {
	cases := []struct {
		name    string
		peerCN  string
		project string
		agent   string
		want    bool
	}{
		// Hand-written CN strings rather than auth.CanonicalAgentCN
		// composer: feeding the composer into both sides of the match
		// reduces the assertion to `x == x` and would pass even if
		// computeCNPinMatch returned `peerCN != ""`. Hand strings keep
		// computeCNPinMatch as the system under test, not the composer.
		{name: "match scoped 3-segment", peerCN: "clawker.foo.bar", project: "foo", agent: "bar", want: true},
		{name: "match unscoped 2-segment", peerCN: "clawker.solo", project: "", agent: "solo", want: true},
		{name: "peer CN differs", peerCN: "clawker.other.bar", project: "foo", agent: "bar", want: false},
		{name: "wrong prefix", peerCN: "notclawker.foo.bar", project: "foo", agent: "bar", want: false},
		{name: "extra segment", peerCN: "clawker.foo.bar.baz", project: "foo", agent: "bar", want: false},
		{name: "missing project segment when scoped", peerCN: "clawker.bar", project: "foo", agent: "bar", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, computeCNPinMatch(tc.peerCN, tc.project, tc.agent))
		})
	}
}

// TestPublishConnected_DeliversPeerIntact pins the bus payload
// contract: peer cert identity fields set on publishConnected land
// on the SessionConnected event a subscriber receives. Subscribers
// driving policy (containment, alerting) consume the typed fields
// directly; a regression that drops a field on the wire wouldn't
// surface from leaf-function tests alone.
func TestPublishConnected_DeliversPeerIntact(t *testing.T) {
	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, bus.Start(ctx))
	defer func() { _ = bus.Close() }()

	sub, ok := overseer.Subscribe[SessionConnected](bus, "test")
	require.True(t, ok)

	d := &Dialer{bus: bus}
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
	peer := peerInfo{
		ChainVerified:  true,
		PeerCN:         "clawker.proj.dev",
		PeerThumbprint: thumb,
	}
	d.publishConnected(ctx, "ctr-prov", "dev", "proj", "10.1.1.5:7700", 3, peer)

	select {
	case ev := <-sub.C:
		assert.Equal(t, "ctr-prov", ev.ContainerID)
		assert.Equal(t, "dev", ev.AgentName)
		assert.Equal(t, "proj", ev.Project)
		assert.Equal(t, "10.1.1.5:7700", ev.Address)
		assert.Equal(t, 3, ev.Attempts)
		assert.Equal(t, "clawker.proj.dev", ev.PeerCN)
		assert.Equal(t, thumb, ev.PeerThumbprint)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SessionConnected event on bus")
	}
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

// --- DialAgent orchestration ---------------------------------------
//
// These tests cover the runDial outer orchestration: dedup map,
// resolveAgent failure → outcomeContainerGone → SessionFailed event
// publication, and ctx-cancel teardown. Leaf functions
// (capturePeerProvenance, fillRegistryProvenance, computeCNPinMatch,
// closeAndCheckLeak) are exercised independently above; this layer
// proves the wiring between them and the overseer event publishers.

// fakeMobyForDialer satisfies mobyclient.APIClient via embedding —
// the embedded nil interface is fine because every test path here
// only exercises ContainerInspect. Any other method invocation panics
// (which is the desired test-fail signal).
type fakeMobyForDialer struct {
	mobyclient.APIClient
	inspectErr error
}

func (f *fakeMobyForDialer) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	if f.inspectErr != nil {
		return mobyclient.ContainerInspectResult{}, f.inspectErr
	}
	// Default: surface errContainerStopped so resolveAgent's terminal
	// "stopped container" path fires. Tests that need a transient
	// inspect error (retry behavior) override inspectErr instead.
	return mobyclient.ContainerInspectResult{}, errContainerStopped
}

// mintLeafKeypair mints a leaf cert + private key signed by parent;
// returns paths to the PEM-encoded files. Distinct from signLeaf
// (which doesn't expose the leaf's private key) because the dialer
// constructor calls tls.LoadX509KeyPair which requires a matching
// pair on disk.
func mintLeafKeypair(t *testing.T, cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (certPath, keyPath string) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &leafKey.PublicKey, parentKey)
	require.NoError(t, err)
	certPath = writeTempFile(t, "cert.pem", mustEncodeCert(t, der))
	keyPath = writeTempFile(t, "key.pem", mustEncodeKey(t, leafKey))
	return certPath, keyPath
}

// newDialerForTest builds a *Dialer with a fresh leaf cert + key
// chained to a fresh CA, a real Overseer bus, and the supplied moby
// fake + registry. The bus is started; caller cancels the returned
// ctx to drain it.
func newDialerForTest(t *testing.T, docker mobyclient.APIClient, agents Registry) (*Dialer, *overseer.Overseer, context.Context, context.CancelFunc) {
	t.Helper()

	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	certPath, keyPath := mintLeafKeypair(t, "cp", caCert, caKey)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	bus := overseer.New(overseer.Options{Logger: logger.Nop()})
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, bus.Start(ctx))

	d, err := New(logger.Nop(), docker, bus, agents, certPath, keyPath, caPool, nil)
	require.NoError(t, err)

	return d, bus, ctx, cancel
}

func TestDialAgent_DedupsConcurrentCallsForSameContainerID(t *testing.T) {
	// Two DialAgent calls for the same containerID must produce
	// exactly ONE SessionFailed event, not two. A regression that
	// forgets the dedup map (or forgets to delete the entry on
	// goroutine exit) would either spin two duplicate goroutines
	// (this test catches that via the Attempts > 1 path) OR leave
	// the dedup key permanent (caught separately by the
	// "redial-after-teardown" test below).
	docker := &fakeMobyForDialer{}
	regMock := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	d, bus, ctx, cancel := newDialerForTest(t, docker, regMock)
	defer cancel()
	defer bus.Close()

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	d.DialAgent(ctx, "container-A")
	d.DialAgent(ctx, "container-A") // dup — should be a no-op

	select {
	case ev := <-sub.C:
		assert.Equal(t, "container-A", ev.ContainerID)
		assert.Equal(t, "container_not_running", ev.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first SessionFailed event")
	}

	// Second event would mean dedup failed. Wait briefly to give a
	// duplicate goroutine a chance to surface, then assert quiet.
	select {
	case ev := <-sub.C:
		t.Fatalf("dedup violated — second SessionFailed arrived: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDialAgent_RedialsAfterTerminalFailureClearsDedup(t *testing.T) {
	// After a terminal SessionFailed, the dedup entry must be cleared
	// so a subsequent DialAgent for the same containerID actually
	// runs. Otherwise a transient docker-daemon hiccup at CP boot
	// would permanently mark the container un-redialable.
	docker := &fakeMobyForDialer{}
	regMock := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	d, bus, ctx, cancel := newDialerForTest(t, docker, regMock)
	defer cancel()
	defer bus.Close()

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	d.DialAgent(ctx, "container-B")
	<-sub.C // first failure event — confirms first goroutine exited

	// Wait for dedup map cleanup (the deferred delete runs after
	// runDial returns; the publishFailed path is part of runDial).
	require.Eventually(t, func() bool {
		d.mu.Lock()
		_, present := d.dialing["container-B"]
		d.mu.Unlock()
		return !present
	}, 1*time.Second, 10*time.Millisecond, "dedup entry must clear after terminal failure")

	d.DialAgent(ctx, "container-B")
	select {
	case ev := <-sub.C:
		assert.Equal(t, "container-B", ev.ContainerID)
	case <-time.After(2 * time.Second):
		t.Fatal("redial after terminal failure produced no event")
	}
}

func TestDialAgent_CtxCancelDuringResolveTearsDownCleanly(t *testing.T) {
	// Cancelling parent ctx mid-attempt must terminate the dial
	// goroutine without publishing SessionFailed. The runDial
	// outcome → switch maps outcomeCtxDone to a silent return.
	// A regression that publishes on shutdown would spam every CP
	// shutdown with a SessionFailed event per running agent.
	docker := &fakeMobyForDialer{
		// resolveAgent will be called AFTER ctx is cancelled in the
		// test body; the resolveAgent path checks ctx.Err() first
		// and returns outcomeCtxDone before this stub is invoked.
		inspectErr: errors.New("should not reach inspect after ctx cancel"),
	}
	regMock := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	d, bus, ctx, cancel := newDialerForTest(t, docker, regMock)
	defer bus.Close()

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	cancel() // pre-cancel; runDial's first ctx.Err() check trips
	d.DialAgent(ctx, "container-C")

	// Channel may close (bus shutdown on ctx cancel) — we want zero
	// SessionFailed VALUES delivered, but a closed-channel signal
	// (ok=false) is fine.
	select {
	case ev, ok := <-sub.C:
		if ok {
			t.Fatalf("ctx-cancel must NOT publish SessionFailed; got: %+v", ev)
		}
	case <-time.After(500 * time.Millisecond):
	}

	// And the dedup entry clears so a subsequent retry (with a fresh
	// ctx) would run.
	require.Eventually(t, func() bool {
		d.mu.Lock()
		_, present := d.dialing["container-C"]
		d.mu.Unlock()
		return !present
	}, 1*time.Second, 10*time.Millisecond)
}

// --- shouldReconnect: post-drain decision -------------------------
//
// The runDial loop's reconnect decision is the load-bearing piece of
// the broken-Session→re-establish path. The full integration is
// hard to unit-test (real moby, real bufconn clawkerd, real TLS),
// but the decision itself is a pure function of (ctx, drainResult).
// These cases pin the matrix:
//
//	ctx alive  + drainGracefulEOF  → reconnect (peer closed, transient)
//	ctx alive  + drainStreamErr    → reconnect (transport break, transient)
//	ctx alive  + drainCtxCanceled  → DON'T reconnect (drain reported teardown)
//	ctx done   + ANY               → DON'T reconnect (CP shutting down)
//
// A regression that drops the ctx.Err() guard would re-enter
// establishWithRetry during CP shutdown and spam SessionConnecting/
// SessionFailed on a draining bus. A regression that drops the
// drainCtxCanceled guard would do the same when the drain itself
// observes ctx.Done() before the loop body checks ctx.Err().

func TestShouldReconnect_AliveCtx_GracefulEOFReconnects(t *testing.T) {
	got := shouldReconnect(context.Background(), drainResult{Outcome: drainGracefulEOF, Reason: "peer closed"})
	assert.True(t, got, "graceful EOF on a live ctx is the reconnect happy path")
}

func TestShouldReconnect_AliveCtx_StreamErrReconnects(t *testing.T) {
	got := shouldReconnect(context.Background(), drainResult{Outcome: drainStreamErr, Reason: "io: broken pipe"})
	assert.True(t, got, "transport break on a live ctx must trigger reconnect")
}

func TestShouldReconnect_AliveCtx_DrainCtxCanceledReturns(t *testing.T) {
	// Defense in depth: drain itself observed ctx.Done() before the
	// loop's own ctx.Err() check could trip. Reconnect must NOT
	// fire — bus is heading down.
	got := shouldReconnect(context.Background(), drainResult{Outcome: drainCtxCanceled})
	assert.False(t, got, "drainCtxCanceled is a teardown signal regardless of ctx state")
}

func TestShouldReconnect_DoneCtx_ReturnsRegardlessOfDrain(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []drainResult{
		{Outcome: drainGracefulEOF, Reason: "peer closed"},
		{Outcome: drainStreamErr, Reason: "io: broken pipe"},
		{Outcome: drainCtxCanceled},
	}
	for _, drain := range cases {
		assert.Falsef(t, shouldReconnect(cancelled, drain),
			"cancelled parent ctx must suppress reconnect for drain=%+v", drain)
	}
}

// panickingMoby fails ContainerInspect with a panic; used by
// TestDialAgent_PanicInRunDial.
type panickingMoby struct {
	mobyclient.APIClient
}

func (panickingMoby) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	panic("synthetic test panic in ContainerInspect")
}

func TestDialAgent_PanicInRunDial_DoesNotCrashCP_PublishesTerminal(t *testing.T) {
	// CP-resilience contract: a panic in the dial goroutine must NOT
	// propagate up the goroutine stack (which would crash CP and
	// strand eBPF programs unsupervised, silently breaking the
	// firewall enforcement boundary). Regression catches:
	//   1. The recover defer deleted from DialAgent.
	//   2. The recover ordering swapped so dedup-cleanup defer runs
	//      before recover (panic still escapes).
	//   3. The synthetic SessionFailed publish removed (worldview
	//      consumers see "Connecting" forever).
	//   4. The dedup map entry not cleared after panic (re-dial
	//      blocked permanently).
	docker := panickingMoby{}
	regMock := &RegistryMock{
		LookupByContainerIDFunc: func(string) (*Entry, error) {
			return nil, ErrUnknownAgent
		},
	}
	d, bus, ctx, cancel := newDialerForTest(t, docker, regMock)
	defer cancel()
	defer bus.Close()

	sub, ok := overseer.Subscribe[SessionFailed](bus, "test")
	require.True(t, ok)

	d.DialAgent(ctx, "container-panic")

	select {
	case ev := <-sub.C:
		assert.Equal(t, "container-panic", ev.ContainerID)
		assert.Contains(t, ev.Reason, "dial_goroutine_panic",
			"recover must publish synthetic SessionFailed with the dial_goroutine_panic classification so subscribers driving containment can branch on it")
	case <-time.After(2 * time.Second):
		t.Fatal("recover failed to publish synthetic SessionFailed — either the panic crashed the goroutine without recover, or the publishFailed call inside the recover handler is wrong")
	}

	require.Eventually(t, func() bool {
		d.mu.Lock()
		_, present := d.dialing["container-panic"]
		d.mu.Unlock()
		return !present
	}, 1*time.Second, 10*time.Millisecond, "dedup entry must clear after panic so subsequent DialAgent calls re-dial")
}
