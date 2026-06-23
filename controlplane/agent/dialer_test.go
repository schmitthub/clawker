package agent_test

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
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	mobyclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/controlplane/agent"
	agentmocks "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
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

// signLeaf issues a leaf cert with cn as Subject.CommonName and
// sanFullName as the urn:clawker:agent: URI SAN value, signed by
// parent (the CA). Callers that don't care about the production
// CN-vs-SAN split (capturePeer chain-mechanics tests) pass cn for
// sanFullName; TestCapturePeer_DistinctCNAndSAN passes the two
// distinct strings to pin that capturePeer reads PeerAgentFullName
// from the SAN, not from Subject.CommonName.
func signLeaf(t *testing.T, cn, sanFullName string, notAfter time.Time, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) ([]byte, *x509.Certificate) {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	agentSAN, err := url.Parse(auth.AgentSANScheme + sanFullName)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{agentSAN},
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
// (PeerAgentFullName, PeerThumbprint) appear on the event payload.

func TestCapturePeer_ValidChain(t *testing.T) {
	const sanFullName = "clawker.proj.dev"
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	leafDER, _ := signLeaf(t, sanFullName, sanFullName, time.Now().Add(time.Hour), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &agent.Dialer{CaPool: pool}

	var peer agent.PeerInfo
	d.CapturePeer([][]byte{leafDER}, &peer)

	assert.True(t, peer.ChainVerified, "trusted-CA chain must verify")
	// PeerAgentFullName is sourced from the URI SAN — assert the SAN
	// value directly. TestCapturePeer_DistinctCNAndSAN pins that it is
	// the SAN and not Subject.CommonName when the two differ.
	assert.Equal(t, sanFullName, peer.PeerAgentFullName)
	assert.Equal(t, sha256.Sum256(leafDER), peer.PeerThumbprint)
	assert.Empty(t, peer.CaptureReason)
}

// TestCapturePeer_ChainVerifyFails covers the permissive-trust
// invariant across both ways leaf.Verify can fail — an untrusted
// signing root and an expired (but correctly-signed) leaf. Both must
// yield ChainVerified=false while STILL populating PeerAgentFullName
// (from the SAN) and PeerThumbprint, and never abort. capturePeer
// routes both through the same leaf.Verify-failed branch; the only
// difference is the underlying x509 reason, which capturePeer collapses
// to "chain verify" either way — so this is one branch, two inputs.
func TestCapturePeer_ChainVerifyFails(t *testing.T) {
	const sanFullName = "clawker.proj.dev"
	cases := []struct {
		name  string
		build func(t *testing.T) (leafDER []byte, pool *x509.CertPool)
	}{
		{
			name: "untrusted root",
			build: func(t *testing.T) ([]byte, *x509.CertPool) {
				wrongCA, wrongKey := genCA(t, "wrong-ca", 24*time.Hour)
				leafDER, _ := signLeaf(t, sanFullName, sanFullName, time.Now().Add(time.Hour), wrongCA, wrongKey)
				trustedCA, _ := genCA(t, "trusted-ca", 24*time.Hour)
				pool := x509.NewCertPool()
				pool.AddCert(trustedCA)
				return leafDER, pool
			},
		},
		{
			name: "expired leaf",
			build: func(t *testing.T) ([]byte, *x509.CertPool) {
				caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
				leafDER, _ := signLeaf(t, sanFullName, sanFullName, time.Now().Add(-time.Minute), caCert, caKey)
				pool := x509.NewCertPool()
				pool.AddCert(caCert)
				return leafDER, pool
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leafDER, pool := tc.build(t)
			d := &agent.Dialer{CaPool: pool}

			var peer agent.PeerInfo
			d.CapturePeer([][]byte{leafDER}, &peer)

			assert.False(t, peer.ChainVerified, "verify failure must yield ChainVerified=false")
			assert.Equal(t, sanFullName, peer.PeerAgentFullName, "SAN must still be captured on verify failure")
			assert.Equal(t, sha256.Sum256(leafDER), peer.PeerThumbprint)
			assert.Contains(t, peer.CaptureReason, "chain verify")
		})
	}
}

func TestCapturePeer_NoCerts_SetsReason(t *testing.T) {
	d := &agent.Dialer{CaPool: x509.NewCertPool()}

	var peer agent.PeerInfo
	d.CapturePeer(nil, &peer)

	assert.False(t, peer.ChainVerified)
	assert.Empty(t, peer.PeerAgentFullName)
	assert.Equal(t, [sha256.Size]byte{}, peer.PeerThumbprint)
	assert.Equal(t, "peer presented no certs", peer.CaptureReason)
}

func TestCapturePeer_BadCertBytes_SetsReason(t *testing.T) {
	d := &agent.Dialer{CaPool: x509.NewCertPool()}

	var peer agent.PeerInfo
	d.CapturePeer([][]byte{[]byte("not a cert")}, &peer)

	assert.False(t, peer.ChainVerified)
	assert.Empty(t, peer.PeerAgentFullName)
	assert.Equal(t, [sha256.Size]byte{}, peer.PeerThumbprint)
	assert.Contains(t, peer.CaptureReason, "leaf parse failed")
}

// TestCapturePeer_DistinctCNAndSAN pins that capturePeer sources
// PeerAgentFullName from the urn:clawker:agent: URI SAN, NOT from
// Subject.CommonName. Production leaves carry CN=clawker-clawkerd
// (a fixed binary identity that yields the same string for every
// agent) and the per-agent AgentFullName in the SAN. A regression
// that reads CN would silently make every connecting agent look
// like clawker-clawkerd to subscribers — this test fails fast on
// that drift.
func TestCapturePeer_DistinctCNAndSAN(t *testing.T) {
	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	const cn = consts.ContainerClawkerd
	const sanFullName = "clawker.myapp.dev"
	leafDER, _ := signLeaf(t, cn, sanFullName, time.Now().Add(time.Hour), caCert, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	d := &agent.Dialer{CaPool: pool}

	var peer agent.PeerInfo
	d.CapturePeer([][]byte{leafDER}, &peer)

	assert.True(t, peer.ChainVerified, "trusted-CA chain must verify")
	assert.Equal(t, sanFullName, peer.PeerAgentFullName,
		"PeerAgentFullName must come from the SAN, not Subject.CommonName")
}

// --- classifyRegistry: registry-row cross-check ---------------------

func TestClassifyRegistry_Match(t *testing.T) {
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
	reg := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agent.Entry, error) {
			return &agent.Entry{
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myproj"),
				ContainerID: id,
				Thumbprint:  thumb,
			}, nil
		},
	}
	d := &agent.Dialer{Agents: reg}

	outcome, _ := d.ClassifyRegistry(thumb, "ctr-1")
	assert.Equal(t, agent.OutcomeRegistryMatch, outcome)
}

func TestClassifyRegistry_Miss(t *testing.T) {
	reg := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agent.Entry, error) {
			return nil, agent.ErrUnknownAgent
		},
	}
	d := &agent.Dialer{Agents: reg}

	outcome, _ := d.ClassifyRegistry(sha256.Sum256([]byte("peer")), "ctr-2")
	assert.Equal(t, agent.OutcomeRegistryMiss, outcome)
}

func TestClassifyRegistry_ThumbprintMismatch(t *testing.T) {
	peerThumb := sha256.Sum256([]byte("peer"))
	rowThumb := sha256.Sum256([]byte("registry"))
	reg := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agent.Entry, error) {
			return &agent.Entry{
				AgentName:   auth.MustAgentName("dev"),
				Project:     auth.MustProjectSlug("myproj"),
				ContainerID: id,
				Thumbprint:  rowThumb,
			}, nil
		},
	}
	d := &agent.Dialer{Agents: reg}

	outcome, _ := d.ClassifyRegistry(peerThumb, "ctr-3")
	assert.Equal(t, agent.OutcomeRegistryThumbprintMismatch, outcome)
}

func TestClassifyRegistry_LookupErrorReturnsNotQueried(t *testing.T) {
	reg := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agent.Entry, error) {
			return nil, errors.New("disk i/o failed")
		},
	}
	d := &agent.Dialer{Agents: reg}

	outcome, detail := d.ClassifyRegistry(sha256.Sum256([]byte("p")), "ctr-5")
	assert.Equal(t, agent.OutcomeRegistryNotQueried, outcome)
	assert.Contains(t, detail, "registry lookup error")
}

// TestClassifyRegistry_MalformedEntryReturnsMiss pins the recovery
// contract for malformed registry rows: a hand-edited or otherwise
// invalid row returns agent.ErrMalformedEntry from LookupByContainerID,
// and the dialer must classify it as Miss so the Register handshake
// drives an evict+rewrite of the row. Treating it as
// NotQueried would publish AgentUntrusted on every reconnect and
// leave the row stranded forever — the dialer's path is the only
// natural trigger for the Register-side cleanup.
func TestClassifyRegistry_MalformedEntryReturnsMiss(t *testing.T) {
	reg := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(id string) (*agent.Entry, error) {
			return nil, fmt.Errorf("scan row: %w", agent.ErrMalformedEntry)
		},
	}
	d := &agent.Dialer{Agents: reg}

	outcome, detail := d.ClassifyRegistry(sha256.Sum256([]byte("p")), "ctr-malformed")
	assert.Equal(t, agent.OutcomeRegistryMiss, outcome)
	assert.Empty(t, detail)
}

func TestClassifyRegistry_NilRegistryReturnsNotQueried(t *testing.T) {
	d := &agent.Dialer{Agents: nil}

	outcome, detail := d.ClassifyRegistry(sha256.Sum256([]byte("p")), "ctr-6")
	assert.Equal(t, agent.OutcomeRegistryNotQueried, outcome)
	assert.Equal(t, "registry not wired", detail)
}

// TestPublishConnected_DeliversPeerIntact pins the event payload
// contract: peer cert identity fields set on publishConnected land on the
// connected AgentEvent a subscriber receives. Subscribers driving policy
// (containment, alerting) consume the typed fields directly; a regression
// that drops a field on the wire wouldn't surface from leaf-function tests
// alone.
func TestPublishConnected_DeliversPeerIntact(t *testing.T) {
	topic := agentmocks.NewAgentTopic(t)
	rec := agentmocks.RecordAgent(topic)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &agent.Dialer{Topic: topic}
	thumb := sha256.Sum256([]byte("peer-cert-bytes"))
	peer := agent.PeerInfo{
		ChainVerified:     true,
		PeerAgentFullName: "clawker.proj.dev",
		PeerThumbprint:    thumb,
	}
	d.PublishConnected(ctx, "ctr-prov", "dev", "proj", "10.1.1.5:7700", 3, peer)

	require.Eventually(t, func() bool {
		_, ok := rec.FirstWith(agent.DialerEventType, agent.ActionConnected)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "timed out waiting for connected AgentEvent")

	ev, _ := rec.FirstWith(agent.DialerEventType, agent.ActionConnected)
	assert.Equal(t, "ctr-prov", ev.Agent.ContainerID)
	assert.Equal(t, "dev", ev.Agent.AgentName)
	assert.Equal(t, "proj", ev.Agent.Project)
	assert.Equal(t, "10.1.1.5:7700", ev.Message.Address)
	assert.Equal(t, 3, ev.Message.Attempts)
	assert.Equal(t, "clawker.proj.dev", ev.Message.PeerAgentFullName)
	assert.Equal(t, thumb, ev.Message.PeerThumbprint)
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
	errs := make([]error, agent.CloseErrCeiling)
	for i := range errs {
		errs[i] = errors.New("transport already shut down")
	}
	c := &fakeCloser{errs: errs}
	count := 0
	d := &agent.Dialer{}

	for i := 1; i <= agent.CloseErrCeiling; i++ {
		bail := d.CloseAndCheckLeak(c, &count, logger.Nop())
		if i < agent.CloseErrCeiling {
			assert.False(t, bail, "must not bail before ceiling (iter %d)", i)
		} else {
			assert.True(t, bail, "must bail at ceiling (iter %d)", i)
		}
	}
	assert.Equal(t, agent.CloseErrCeiling, count)
	assert.Equal(t, agent.CloseErrCeiling, c.calls)
}

func TestCloseAndCheckLeak_SuccessResetsCounter(t *testing.T) {
	c := &fakeCloser{errs: []error{
		errors.New("hiccup"),
		errors.New("hiccup"),
		nil,
		errors.New("hiccup"),
	}}
	count := 0
	d := &agent.Dialer{}

	require.False(t, d.CloseAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 1, count)
	require.False(t, d.CloseAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 2, count)
	// Successful close — counter resets.
	require.False(t, d.CloseAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 0, count)
	// Subsequent failure starts from 1, not from 3.
	require.False(t, d.CloseAndCheckLeak(c, &count, logger.Nop()))
	require.Equal(t, 1, count)
}

func TestCloseAndCheckLeak_LogsCloseFailure(t *testing.T) {
	// A close failure must emit a greppable structured event: operators
	// grep the structured log surface for FD-leak signals (CP resilience
	// doctrine — structured logs are the only operator-visible surface).
	// The count/bail behavior is covered by BailsAfterCeiling and
	// SuccessResetsCounter; this only pins that the event token is
	// emitted, not the field values (which would be a brittle
	// log-format assertion duplicating those tests).
	c := &fakeCloser{errs: []error{errors.New("transport already shut down")}}
	count := 0
	var buf bytes.Buffer
	d := &agent.Dialer{}

	d.CloseAndCheckLeak(c, &count, logger.NewWriter(&buf))

	assert.Contains(t, buf.String(), "agentdial_conn_close_failed")
}

// --- DialAgent orchestration ---------------------------------------
//
// These tests cover the runDial outer orchestration: dedup map,
// resolveAgent failure → outcomeContainerGone → SessionFailed event
// publication, and ctx-cancel teardown. Leaf functions
// (capturePeer, classifyRegistry, closeAndCheckLeak) are exercised
// independently above; this layer proves the wiring between them and
// the AgentEvent publishers.

// fakeMobyForDialer satisfies mobyclient.APIClient via embedding —
// the embedded nil interface is fine because every test path here
// only exercises ContainerInspect. Any other method invocation panics
// (which is the desired test-fail signal).
type fakeMobyForDialer struct {
	mobyclient.APIClient
	inspectErr error
	// onInspect, when non-nil, is invoked at the start of every
	// ContainerInspect call. The dedup test uses it as a channel gate
	// to hold the first dial goroutine inside resolveAgent (dedup key
	// held) until the second DialAgent call has been issued, making the
	// concurrent-in-flight dedup assertion deterministic rather than a
	// goroutine-scheduling coin-flip.
	onInspect func()
}

func (f *fakeMobyForDialer) ContainerInspect(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	if f.onInspect != nil {
		f.onInspect()
	}
	if f.inspectErr != nil {
		return mobyclient.ContainerInspectResult{}, f.inspectErr
	}
	// Default: surface agent.ErrContainerStopped so resolveAgent's terminal
	// "stopped container" path fires. Tests that need a transient
	// inspect error (retry behavior) override inspectErr instead.
	return mobyclient.ContainerInspectResult{}, agent.ErrContainerStopped
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

// newDialerForTest builds a *Dialer with a fresh leaf cert + key chained
// to a fresh CA, a real agent Topic, and the supplied moby fake +
// registry. Returns a recorder already subscribed to the topic and a ctx
// the caller cancels to tear down.
func newDialerForTest(t *testing.T, docker mobyclient.APIClient, agents agent.Registry) (*agent.Dialer, *agentmocks.AgentRecorder, context.Context, context.CancelFunc) {
	t.Helper()

	caCert, caKey := genCA(t, "clawker-ca", 24*time.Hour)
	certPath, keyPath := mintLeafKeypair(t, "cp", caCert, caKey)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	topic := agentmocks.NewAgentTopic(t)
	rec := agentmocks.RecordAgent(topic)
	ctx, cancel := context.WithCancel(context.Background())

	d, err := agent.NewDialer(logger.Nop(), docker, topic, agents, certPath, keyPath, caPool, nil)
	require.NoError(t, err)

	return d, rec, ctx, cancel
}

// awaitFailed waits until at least one session-failed AgentEvent has been
// recorded and returns the first one. Fails the test on timeout.
func awaitFailed(t *testing.T, rec *agentmocks.AgentRecorder) agent.AgentEvent {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := rec.FirstWith(agent.DialerEventType, agent.ActionFailed)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "timed out waiting for session-failed AgentEvent")
	ev, _ := rec.FirstWith(agent.DialerEventType, agent.ActionFailed)
	return ev
}

func TestDialAgent_DedupsConcurrentCallsForSameContainerID(t *testing.T) {
	// Two DialAgent calls for the same containerID must produce exactly
	// ONE SessionFailed event, not two. The dedup map only guards dials
	// that are concurrently in-flight, so the test has to guarantee the
	// first dial goroutine is still running (its dedup key still held)
	// when the second call arrives. onInspect parks that goroutine
	// inside resolveAgent until the second DialAgent has been issued.
	//
	// Without the gate the agent.ErrContainerStopped resolve path returns on
	// attempt 1 with no backoff, the first goroutine can run to terminal
	// failure and clear its dedup key before the second call takes the
	// lock, and the second call then spawns its own goroutine — turning
	// the dedup assertion into a goroutine-scheduling coin-flip that is
	// flaky on multi-core CI. The channel gate forces the overlap, so
	// the dedup is exercised deterministically on any core count.
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	docker := &fakeMobyForDialer{
		onInspect: func() {
			entered <- struct{}{}
			<-release
		},
	}
	regMock := &agentmocks.RegistryMock{
		LookupByContainerIDFunc: func(string) (*agent.Entry, error) {
			return nil, agent.ErrUnknownAgent
		},
	}
	d, rec, ctx, cancel := newDialerForTest(t, docker, regMock)
	defer cancel()

	d.DialAgent(ctx, "container-A")
	select {
	case <-entered: // first dial parked in resolveAgent — dedup key held
	case <-time.After(2 * time.Second):
		t.Fatal("first dial goroutine never reached ContainerInspect")
	}
	d.DialAgent(ctx, "container-A") // dup — key present → guaranteed no-op
	close(release)                  // let the first dial run to terminal failure

	ev := awaitFailed(t, rec)
	assert.Equal(t, "container-A", ev.Agent.ContainerID)
	assert.Equal(t, "container_not_running", ev.Message.Detail)

	// A second event would mean the dup spawned its own goroutine. With
	// the gate forcing the overlap this is deterministic, not a race: if
	// the dedup map were dropped, the dup's goroutine would also pass
	// through the (now-closed) gate and publish a second session-failed
	// event. Allow a beat for any erroneous second publish to land.
	time.Sleep(200 * time.Millisecond)
	failed := rec.WithAction(agent.DialerEventType, agent.ActionFailed)
	require.Len(t, failed, 1, "dedup violated — a second session-failed event arrived")
}

// --- shouldReconnect: post-drain decision -------------------------
//
// The runDial loop's reconnect decision is the load-bearing piece of
// the broken-Session→re-establish path. The full integration is hard
// to unit-test (real moby, real bufconn clawkerd, real TLS), but the
// decision itself is a pure function of (ctx, drainResult). The matrix:
//
//	ctx alive + drainGracefulEOF  → reconnect (peer closed, transient)
//	ctx alive + drainStreamErr    → reconnect (transport break, transient)
//	ctx alive + drainCtxCanceled  → DON'T reconnect (drain reported teardown)
//	ctx done  + ANY               → DON'T reconnect (CP shutting down)
//
// A regression that drops the ctx.Err() guard would re-enter
// establishWithRetry during CP shutdown and spam SessionConnecting/
// SessionFailed on a draining bus. A regression that drops the
// drainCtxCanceled guard would do the same when the drain itself
// observes ctx.Done() before the loop body checks ctx.Err().
func TestShouldReconnect(t *testing.T) {
	cases := []struct {
		name    string
		ctxDone bool
		drain   agent.DrainResult
		want    bool
	}{
		{"alive+gracefulEOF reconnects", false, agent.DrainResult{Outcome: agent.DrainGracefulEOF, Reason: "peer closed"}, true},
		{"alive+streamErr reconnects", false, agent.DrainResult{Outcome: agent.DrainStreamErr, Reason: "io: broken pipe"}, true},
		// Defense in depth: drain observed ctx.Done() before the loop's
		// own ctx.Err() check could trip — teardown regardless of ctx.
		{"alive+drainCtxCanceled tears down", false, agent.DrainResult{Outcome: agent.DrainCtxCanceled}, false},
		// A cancelled parent ctx suppresses reconnect for every drain
		// outcome (CP shutting down).
		{"done+gracefulEOF tears down", true, agent.DrainResult{Outcome: agent.DrainGracefulEOF, Reason: "peer closed"}, false},
		{"done+streamErr tears down", true, agent.DrainResult{Outcome: agent.DrainStreamErr, Reason: "io: broken pipe"}, false},
		{"done+drainCtxCanceled tears down", true, agent.DrainResult{Outcome: agent.DrainCtxCanceled}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.ctxDone {
				cancel()
			}
			assert.Equal(t, tc.want, agent.ShouldReconnect(ctx, tc.drain))
		})
	}
}
