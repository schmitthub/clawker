package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// hydraTokenTimeout bounds the Hydra token-exchange round trip. 10s
// covers a slow first-boot DNS resolution + TLS handshake without
// letting a wedged Hydra block the Register flow indefinitely.
const hydraTokenTimeout = 10 * time.Second

// registerRPCTimeout bounds the AgentService.Register call from
// clawkerd's side: dial + bearer-creds + Register handler latency.
// Generous because the handler does docker-inspect + sqlite write.
const registerRPCTimeout = 15 * time.Second

// registerCoordinator serializes the CP-driven Register handshake.
// CP may send RegisterRequired more than once if the Session is
// retried before the first Register completes; the coordinator
// guards against duplicate Hydra exchanges + duplicate AgentService
// calls.
//
// Short-circuit policy: the cached outcome is returned only when the
// PRIOR attempt actually consumed the single-use Hydra
// client_assertion JWT — i.e., Hydra parsed and responded (success or
// HTTP-level rejection). A pre-Hydra failure (network blip, TLS
// handshake error, ctx timeout before request reached Hydra) leaves
// the assertion still usable; the next CP-driven RegisterRequired
// retries from scratch. Without this distinction a transient blip on
// the very first attempt would burn the assertion forever (in this
// process), and every subsequent retry would return the cached
// transport error even though the assertion JWT is still valid at
// Hydra.
//
// Lock is held across runOnce so concurrent RegisterRequired
// dispatches serialize cleanly — Run is a goroutine entry point, so
// blocking it for the duration of one attempt is fine.
// exchangeFunc is the seam tests inject to replace the real Hydra
// exchange. Production wiring threads exchangeAssertion through so the
// coordinator's behavior is identical; tests inject a closure that
// returns (token, consumed, err) deterministically.
type exchangeFunc func(ctx context.Context, tokenURL, assertion string, tlsCfg *tls.Config) (string, bool, error)

type registerCoordinator struct {
	mu        sync.Mutex
	consumed  bool
	doneOK    bool
	doneErr   string
	boot      *bootstrap
	hydraURL  string
	agentAddr string
	agentName string
	project   string
	// exchange is set to exchangeAssertion in production via
	// newRegisterCoordinator; tests override it via newCoordinatorWithExchange.
	exchange exchangeFunc
	// dialAndRegister is the post-Hydra step (mTLS dial + Register
	// RPC). Defaults to the real implementation; tests can short-
	// circuit it to avoid standing up a CP-side gRPC server.
	dialAndRegister func(ctx context.Context, log *logger.Logger, token string) (bool, string)
}

func newRegisterCoordinator(boot *bootstrap, hydraURL, agentAddr, agentName, project string) *registerCoordinator {
	rc := &registerCoordinator{
		boot:      boot,
		hydraURL:  hydraURL,
		agentAddr: agentAddr,
		agentName: agentName,
		project:   project,
		exchange:  exchangeAssertion,
	}
	rc.dialAndRegister = rc.realDialAndRegister
	return rc
}

// Run drives one Register handshake in response to a CP-sent
// RegisterRequired. Returns (ok, errorMessage) suitable for the
// RegisterDone Response payload.
//
// First call performs the actual Hydra exchange + dial + Register.
// Subsequent calls return the cached outcome ONLY when the prior
// attempt actually consumed the single-use Hydra assertion (Hydra
// parsed the request and responded — success or HTTP rejection).
// Pre-Hydra transport failures (network, TLS, ctx timeout before
// request reached Hydra) do NOT short-circuit; the next call retries.
func (rc *registerCoordinator) Run(ctx context.Context, log *logger.Logger) (bool, string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.consumed {
		log.Info().
			Bool("ok", rc.doneOK).
			Str("event", "register_replay_short_circuit").
			Msg("RegisterRequired received again; assertion already consumed, replaying cached outcome")
		return rc.doneOK, rc.doneErr
	}

	ok, errMsg, consumed := rc.runOnce(ctx, log)
	if consumed {
		rc.consumed = true
		rc.doneOK = ok
		rc.doneErr = errMsg
	} else {
		log.Warn().
			Str("event", "register_assertion_unconsumed").
			Str("error", errMsg).
			Msg("Register attempt failed before reaching Hydra; assertion still usable for retry")
	}
	return ok, errMsg
}

// runOnce returns (ok, errMsg, consumed). consumed is true iff the
// Hydra assertion was actually presented to Hydra and parsed — that's
// the moment a single-use JWT becomes spent. A return of (_, _, false)
// means the assertion is still usable and the caller may retry.
func (rc *registerCoordinator) runOnce(ctx context.Context, log *logger.Logger) (bool, string, bool) {
	if rc.hydraURL == "" {
		log.Error().Str("event", "register_env_missing").Msg("CLAWKER_CP_HYDRA_URL unset; cannot exchange assertion")
		return false, "CLAWKER_CP_HYDRA_URL unset; cannot exchange assertion", false
	}
	if rc.agentAddr == "" {
		log.Error().Str("event", "register_env_missing").Msg("CLAWKER_CP_AGENT_ADDR unset; cannot dial AgentService")
		return false, "CLAWKER_CP_AGENT_ADDR unset; cannot dial AgentService", false
	}

	// 1. Hydra token exchange.
	tokenURL := strings.TrimRight(rc.hydraURL, "/") + "/oauth2/token"
	tokenTLS, err := buildTokenTLSConfig(rc.boot.CACertPEM)
	if err != nil {
		log.Error().Err(err).Str("event", "token_tls_config_failed").Msg("build token TLS config")
		return false, "token TLS config: " + err.Error(), false
	}

	log.Info().Str("event", "token_exchange_attempt").Str("url", tokenURL).Msg("posting client_assertion to Hydra")
	tokenCtx, tokenCancel := context.WithTimeout(ctx, hydraTokenTimeout)
	defer tokenCancel()
	token, consumed, err := rc.exchange(tokenCtx, tokenURL, rc.boot.Assertion, tokenTLS)
	if err != nil {
		log.Error().Err(err).
			Bool("assertion_consumed", consumed).
			Str("event", "token_exchange_failed").
			Msg("hydra token exchange")
		return false, "hydra token exchange: " + err.Error(), consumed
	}
	log.Info().Str("event", "token_acquired").Msg("Hydra issued access token")

	// 2 + 3. mTLS-dial CP AgentService and call Register. Anything
	// past the Hydra exchange has spent the assertion.
	ok, errMsg := rc.dialAndRegister(ctx, log, token)
	return ok, errMsg, true
}

func (rc *registerCoordinator) realDialAndRegister(ctx context.Context, log *logger.Logger, token string) (bool, string) {
	dialTLS, err := buildDialTLSConfig(rc.boot.CertPEM, rc.boot.KeyPEM, rc.boot.CACertPEM)
	if err != nil {
		log.Error().Err(err).Str("event", "register_dial_tls_failed").Msg("build dial TLS config")
		return false, "dial TLS config: " + err.Error()
	}

	log.Info().Str("event", "register_dial").Str("addr", rc.agentAddr).Msg("dialing CP agent listener")
	conn, err := grpc.NewClient(
		rc.agentAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(dialTLS)),
		grpc.WithPerRPCCredentials(newBearerCreds(token)),
	)
	if err != nil {
		log.Error().Err(err).Str("event", "register_dial_failed").Msg("grpc.NewClient")
		return false, "dial CP agent listener: " + err.Error()
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Error().Err(cerr).Str("event", "register_conn_close_failed").Msg("close")
		}
	}()

	rpcCtx, rpcCancel := context.WithTimeout(ctx, registerRPCTimeout)
	defer rpcCancel()
	client := agentv1.NewAgentServiceClient(conn)
	if _, err := client.Register(rpcCtx, &agentv1.RegisterRequest{
		AgentName: rc.agentName,
		Project:   rc.project,
	}); err != nil {
		log.Error().Err(err).Str("event", "register_rpc_failed").Msg("AgentService.Register")
		return false, "AgentService.Register: " + err.Error()
	}
	log.Info().Str("event", "register_rpc_ok").Msg("AgentService.Register returned Welcome")
	return true, ""
}

// bearerCreds attaches "authorization: Bearer <token>" to every
// outgoing RPC — unary AND streaming. PerRPCCredentials is the only
// path that covers both surfaces; a unary-only interceptor would
// silently skip future server-stream RPCs.
type bearerCreds struct {
	token string
}

func (c bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

func (bearerCreds) RequireTransportSecurity() bool { return true }

func newBearerCreds(token string) credentials.PerRPCCredentials {
	return bearerCreds{token: token}
}

func buildTokenTLSConfig(caPEM []byte) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA PEM did not parse")
	}
	return &tls.Config{
		RootCAs:    pool,
		ServerName: consts.ContainerCP,
		MinVersion: tls.VersionTLS13,
	}, nil
}

func buildDialTLSConfig(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent leaf keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA PEM did not parse")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{leaf},
		RootCAs:      pool,
		ServerName:   consts.ContainerCP,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// exchangeAssertion posts the CLI-signed client_assertion JWT to
// Hydra's /oauth2/token endpoint and returns (token, consumed, err).
// consumed is true iff the request reached Hydra and Hydra responded
// (any HTTP status) — at that point Hydra has parsed the JWT and the
// single-use JTI is burned. consumed is false when the request never
// reached Hydra (request build, DNS, TCP, TLS, or ctx error before the
// HTTP response landed). Callers branch on consumed to decide whether
// retrying with the same assertion is meaningful.
func exchangeAssertion(ctx context.Context, tokenURL, assertion string, tlsCfg *tls.Config) (string, bool, error) {
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
		"scope":                 {string(agentv1.ScopeSelfRegister)},
	}

	httpClient := &http.Client{
		Timeout:   hydraTokenTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// httpClient.Do error: request never produced an HTTP response.
	// Assertion is NOT consumed.
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("post to %s: %w", tokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// Body read error: Hydra issued a response (consumed) but the
		// transport failed mid-stream. Still consumed.
		return "", true, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", true, fmt.Errorf("hydra returned %d: %s", resp.StatusCode, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", true, fmt.Errorf("decode response: %w", err)
	}
	if out.AccessToken == "" {
		return "", true, fmt.Errorf("hydra returned empty access_token")
	}
	if out.TokenType != "" && !strings.EqualFold(out.TokenType, "Bearer") {
		return "", true, fmt.Errorf("hydra returned unexpected token_type %q (expected Bearer)", out.TokenType)
	}
	return out.AccessToken, true, nil
}
