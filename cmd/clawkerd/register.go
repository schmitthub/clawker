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
// calls. Once a Register has been attempted (success OR failure),
// subsequent triggers short-circuit:
//
//   - The Hydra client_assertion JWT is single-use; a second exchange
//     would fail anyway.
//   - The agentregistry row is idempotent on matching thumbprint, so
//     a second attempt wouldn't write a different row, but it would
//     burn the network round-trip.
//
// The coordinator's exit condition is process lifetime — once
// triggered, a clawkerd process won't re-enter Register.
type registerCoordinator struct {
	mu        sync.Mutex
	triggered bool
	doneOK    bool
	doneErr   string
	boot      *bootstrap
	hydraURL  string
	agentAddr string
	agentName string
	project   string
}

func newRegisterCoordinator(boot *bootstrap, hydraURL, agentAddr, agentName, project string) *registerCoordinator {
	return &registerCoordinator{
		boot:      boot,
		hydraURL:  hydraURL,
		agentAddr: agentAddr,
		agentName: agentName,
		project:   project,
	}
}

// Run drives one Register handshake in response to a CP-sent
// RegisterRequired. Returns (ok, errorMessage) suitable for the
// RegisterDone Response payload.
//
// First call performs the actual Hydra exchange + dial + Register.
// Subsequent calls return the recorded outcome without re-running —
// the assertion JWT is single-use; a re-attempt is guaranteed to
// fail at Hydra. The coordinator's typed result is therefore the
// authoritative answer for the rest of the process lifetime.
func (rc *registerCoordinator) Run(ctx context.Context, log *logger.Logger) (bool, string) {
	rc.mu.Lock()
	if rc.triggered {
		ok, errMsg := rc.doneOK, rc.doneErr
		rc.mu.Unlock()
		log.Info().
			Bool("ok", ok).
			Str("event", "register_replay_short_circuit").
			Msg("RegisterRequired received again; replaying recorded outcome")
		return ok, errMsg
	}
	rc.triggered = true
	rc.mu.Unlock()

	ok, errMsg := rc.runOnce(ctx, log)

	rc.mu.Lock()
	rc.doneOK = ok
	rc.doneErr = errMsg
	rc.mu.Unlock()
	return ok, errMsg
}

func (rc *registerCoordinator) runOnce(ctx context.Context, log *logger.Logger) (bool, string) {
	if rc.hydraURL == "" {
		return false, "CLAWKER_CP_HYDRA_URL unset; cannot exchange assertion"
	}
	if rc.agentAddr == "" {
		return false, "CLAWKER_CP_AGENT_ADDR unset; cannot dial AgentService"
	}

	// 1. Hydra token exchange.
	tokenURL := strings.TrimRight(rc.hydraURL, "/") + "/oauth2/token"
	tokenTLS, err := buildTokenTLSConfig(rc.boot.CACertPEM)
	if err != nil {
		return false, "token TLS config: " + err.Error()
	}

	log.Info().Str("event", "token_exchange_attempt").Str("url", tokenURL).Msg("posting client_assertion to Hydra")
	tokenCtx, tokenCancel := context.WithTimeout(ctx, hydraTokenTimeout)
	defer tokenCancel()
	token, err := exchangeAssertion(tokenCtx, tokenURL, rc.boot.Assertion, tokenTLS)
	if err != nil {
		log.Error().Err(err).Str("event", "token_exchange_failed").Msg("hydra token exchange")
		return false, "hydra token exchange: " + err.Error()
	}
	log.Info().Str("event", "token_acquired").Msg("Hydra issued access token")

	// 2. mTLS-dial CP AgentService with the per-agent leaf cert +
	// bearer creds (token covers unary + future streaming RPCs).
	dialTLS, err := buildDialTLSConfig(rc.boot.CertPEM, rc.boot.KeyPEM, rc.boot.CACertPEM)
	if err != nil {
		return false, "dial TLS config: " + err.Error()
	}

	log.Info().Str("event", "register_dial").Str("addr", rc.agentAddr).Msg("dialing CP agent listener")
	conn, err := grpc.NewClient(
		rc.agentAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(dialTLS)),
		grpc.WithPerRPCCredentials(newBearerCreds(token)),
	)
	if err != nil {
		return false, "dial CP agent listener: " + err.Error()
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Error().Err(cerr).Str("event", "register_conn_close_failed").Msg("close")
		}
	}()

	// 3. AgentService.Register call.
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
// Hydra's /oauth2/token endpoint and returns the access token. Single
// shot — the assertion is single-use; subsequent runs of the same
// process must short-circuit via the registerCoordinator.
func exchangeAssertion(ctx context.Context, tokenURL, assertion string, tlsCfg *tls.Config) (string, error) {
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
		"scope":                 {consts.ScopeAgentSelfRegister},
	}

	httpClient := &http.Client{
		Timeout:   hydraTokenTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post to %s: %w", tokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hydra returned %d: %s", resp.StatusCode, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("hydra returned empty access_token")
	}
	if out.TokenType != "" && !strings.EqualFold(out.TokenType, "Bearer") {
		return "", fmt.Errorf("hydra returned unexpected token_type %q (expected Bearer)", out.TokenType)
	}
	return out.AccessToken, nil
}
