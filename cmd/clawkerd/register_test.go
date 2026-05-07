package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

// validPEMs returns a valid CA cert PEM + a leaf cert/key keypair PEM
// suitable for buildTokenTLSConfig + buildDialTLSConfig parsing. The
// material is throwaway — none of these tests actually dial a Hydra or
// CP server; we just need the inner config builders to succeed so
// runOnce reaches the exchange/dialAndRegister seams.
func validPEMs(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = caPEM
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return caPEM, certPEM, keyPEM
}

// stubExchange returns an exchangeFunc that records call count and
// returns a fixed (token, consumed, err) on every call.
func stubExchange(token string, consumed bool, err error, calls *int32) exchangeFunc {
	return func(_ context.Context, _ string, _ string, _ *tls.Config) (string, bool, error) {
		atomic.AddInt32(calls, 1)
		return token, consumed, err
	}
}

// stubDialAndRegister returns a closure suitable for
// rc.dialAndRegister. Tests override the post-Hydra path so they can
// drive Run end-to-end without standing up a CP gRPC server.
func stubDialAndRegister(ok bool, errMsg string) func(context.Context, *logger.Logger, string) (bool, string) {
	return func(context.Context, *logger.Logger, string) (bool, string) {
		return ok, errMsg
	}
}

func newTestCoordinator(t *testing.T, exchange exchangeFunc, dialFn func(context.Context, *logger.Logger, string) (bool, string)) *registerCoordinator {
	t.Helper()
	caPEM, certPEM, keyPEM := validPEMs(t)
	rc := newRegisterCoordinator(&bootstrap{
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		CACertPEM: caPEM,
		Assertion: "fake.jwt.assertion",
	}, "https://hydra.test/oauth2/token", "127.0.0.1:9999", "agent", "proj")
	rc.exchange = exchange
	rc.dialAndRegister = dialFn
	return rc
}

// TestRegisterCoordinator_HydraNotConsumed_AllowsRetry pins the
// load-bearing semantics: when the request never reaches Hydra (DNS,
// TCP, TLS, or pre-write transport error), the assertion is NOT
// considered consumed and a subsequent Run retries from scratch.
func TestRegisterCoordinator_HydraNotConsumed_AllowsRetry(t *testing.T) {
	var calls int32
	rc := newTestCoordinator(t,
		stubExchange("", false, errors.New("connection refused"), &calls),
		stubDialAndRegister(false, "unused"))

	ok1, _ := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok1)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls))

	// Second Run must retry — assertion never consumed.
	ok2, _ := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok2)
	require.EqualValues(t, 2, atomic.LoadInt32(&calls), "transport failure must NOT consume assertion")

	rc.mu.Lock()
	assert.False(t, rc.consumed)
	rc.mu.Unlock()
}

// TestRegisterCoordinator_HydraReturnsHTTP_ConsumesAssertion: any HTTP
// response from Hydra (incl. 4xx/5xx) consumes the assertion JTI.
// Subsequent Run must short-circuit with the cached error.
func TestRegisterCoordinator_HydraReturnsHTTP_ConsumesAssertion(t *testing.T) {
	var calls int32
	rc := newTestCoordinator(t,
		stubExchange("", true, errors.New("hydra returned 401"), &calls),
		stubDialAndRegister(false, "unused"))

	ok1, err1 := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok1)
	require.NotEmpty(t, err1)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls))

	ok2, err2 := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok2)
	require.Equal(t, err1, err2, "second Run must return cached error")
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "second Run must NOT re-hit Hydra")

	rc.mu.Lock()
	assert.True(t, rc.consumed)
	rc.mu.Unlock()
}

// TestRegisterCoordinator_HappyPath: token exchange + Register both
// succeed → Run returns (true, "") and caches the success.
func TestRegisterCoordinator_HappyPath(t *testing.T) {
	var calls int32
	rc := newTestCoordinator(t,
		stubExchange("test-bearer-token", true, nil, &calls),
		stubDialAndRegister(true, ""))

	ok1, err1 := rc.Run(context.Background(), logger.Nop())
	require.True(t, ok1)
	require.Empty(t, err1)

	// Second Run short-circuits with cached success.
	ok2, _ := rc.Run(context.Background(), logger.Nop())
	require.True(t, ok2)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "second Run must short-circuit")
}

// TestRegisterCoordinator_PostHydraFailure_StillConsumed: Hydra
// returned a token, but the dial / Register RPC failed. Assertion is
// still consumed (we already exchanged it). Subsequent Run must
// short-circuit with the cached failure.
func TestRegisterCoordinator_PostHydraFailure_StillConsumed(t *testing.T) {
	var hydraCalls int32
	var dialCalls int32
	rc := newTestCoordinator(t,
		stubExchange("token", true, nil, &hydraCalls),
		func(context.Context, *logger.Logger, string) (bool, string) {
			atomic.AddInt32(&dialCalls, 1)
			return false, "AgentService.Register: PermissionDenied"
		})

	ok1, _ := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok1)
	require.EqualValues(t, 1, atomic.LoadInt32(&hydraCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&dialCalls))

	ok2, _ := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok2)
	require.EqualValues(t, 1, atomic.LoadInt32(&hydraCalls), "second Run must short-circuit")
	require.EqualValues(t, 1, atomic.LoadInt32(&dialCalls))
}

// TestRegisterCoordinator_EnvMissing_NotConsumed: pre-Hydra config
// failures (CLAWKER_CP_HYDRA_URL unset) leave the assertion usable.
func TestRegisterCoordinator_EnvMissing_NotConsumed(t *testing.T) {
	caPEM, certPEM, keyPEM := validPEMs(t)
	rc := newRegisterCoordinator(&bootstrap{
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		CACertPEM: caPEM,
		Assertion: "fake",
	}, "" /* hydraURL unset */, "127.0.0.1:1", "agent", "proj")

	ok, errMsg := rc.Run(context.Background(), logger.Nop())
	require.False(t, ok)
	assert.Contains(t, errMsg, "CLAWKER_CP_HYDRA_URL")

	rc.mu.Lock()
	defer rc.mu.Unlock()
	assert.False(t, rc.consumed)
}

// TestRegisterCoordinator_ConcurrentRuns_Serialize: many goroutines
// firing Run concurrently produce exactly ONE Hydra call, with every
// caller seeing the identical outcome. Detects unsynchronized state
// under -race.
func TestRegisterCoordinator_ConcurrentRuns_Serialize(t *testing.T) {
	var hydraCalls int32
	rc := newTestCoordinator(t,
		stubExchange("", true, errors.New("hydra returned 400"), &hydraCalls),
		stubDialAndRegister(false, "unused"))

	const n = 16
	var wg sync.WaitGroup
	results := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errMsg := rc.Run(context.Background(), logger.Nop())
			results[i] = errMsg
		}(i)
	}
	wg.Wait()

	require.EqualValues(t, 1, atomic.LoadInt32(&hydraCalls), "concurrent Runs must collapse to a single Hydra call")
	for i := 1; i < n; i++ {
		require.Equal(t, results[0], results[i], "all callers must see identical outcome")
	}
}

// --- exchangeAssertion direct unit tests --------------------------------

// TestExchangeAssertion_TransportFailure_NotConsumed: a request that
// errors before Hydra produces a response returns consumed=false.
func TestExchangeAssertion_TransportFailure_NotConsumed(t *testing.T) {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, consumed, err := exchangeAssertion(ctx, "https://127.0.0.1:1/oauth2/token", "fake", tlsCfg)
	require.Error(t, err)
	assert.False(t, consumed)
}

// TestExchangeAssertion_HTTPLevelError_Consumed: a non-200 from Hydra
// counts as the assertion being consumed (Hydra parsed the JWT).
func TestExchangeAssertion_HTTPLevelError_Consumed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tlsCfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	_, consumed, err := exchangeAssertion(context.Background(), srv.URL+"/oauth2/token", "fake", tlsCfg)
	require.Error(t, err)
	assert.True(t, consumed, "HTTP-level error must mark assertion consumed")
}

// TestExchangeAssertion_HappyPath returns the access token.
func TestExchangeAssertion_HappyPath(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "test-bearer-token",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tlsCfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	tok, consumed, err := exchangeAssertion(context.Background(), srv.URL+"/oauth2/token", "fake", tlsCfg)
	require.NoError(t, err)
	assert.Equal(t, "test-bearer-token", tok)
	assert.True(t, consumed)
}

// TestExchangeAssertion_RejectsNonBearerTokenType: a non-Bearer
// token_type means clawkerd cannot construct a valid Authorization
// header — reject up front rather than sending malformed metadata.
func TestExchangeAssertion_RejectsNonBearerTokenType(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "test-token",
			"token_type":   "Mac",
		})
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tlsCfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	_, consumed, err := exchangeAssertion(context.Background(), srv.URL+"/oauth2/token", "fake", tlsCfg)
	require.Error(t, err)
	assert.True(t, consumed, "Hydra responded; assertion is consumed")
}

// TestBearerCreds_RequiresTLS: PerRPCCredentials covers unary AND
// streaming. RequireTransportSecurity=false would silently allow
// plain-TCP exchange of bearer tokens — pin the contract.
func TestBearerCreds_RequiresTLS(t *testing.T) {
	c := newBearerCreds("test-token")
	require.True(t, c.RequireTransportSecurity())
	md, err := c.GetRequestMetadata(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Bearer test-token", md["authorization"])
}
