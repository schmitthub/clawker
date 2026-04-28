// Command clawkerd is the per-container agent daemon. It runs as a
// backgrounded child of the container entrypoint shell, owns the
// per-container ClawkerdService listener that the CP dials for command
// dispatch, and idles for the container's lifetime.
//
// Boot sequence:
//
//  1. Read bootstrap material delivered by the CLI to
//     consts.BootstrapDir (cert.pem, key.pem, ca.pem, assertion.jwt,
//     verifier). All five files are written by the CLI between
//     ContainerCreate and ContainerStart; the assertion + verifier are
//     unused by clawkerd today (CLI is the registry writer) but stay
//     on disk for the agent→CP RPCs landing in upcoming branches.
//  2. Start the ClawkerdService mTLS listener on
//     consts.DefaultClawkerdPort. The listener pins peer CN to
//     consts.ContainerCP so no other agent's CA-signed cert can
//     connect.
//  3. POST the CLI-signed client_assertion to Hydra → access token
//     bound to the clawker-agent client. Wired so future agent→CP
//     calls have the auth chain ready; clawkerd does not call any
//     AgentService RPC in this branch.
//  4. mTLS-dial the CP agent listener with the per-agent leaf cert.
//     Same rationale as step 3 — agentClient is constructed and held
//     for future RPCs.
//  5. Idle on ctx.Done — daemon lifetime is bound to container
//     lifetime, NOT to any single CP connection. The :7700 listener
//     stays up for CP to dial Session repeatedly; CP→clawkerd
//     connection breaks are logged but do not kill the daemon.
//
// Identity / registration: the CLI writes the agentregistry row at
// container CREATE time (via host-side sqlite). CP reads the registry
// when it dials clawkerd's listener and verifies the peer cert
// thumbprint against the registered row — provenance attestation flows
// entirely through that path, no clawkerd outbound RPC required.
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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// hydraTokenTimeout bounds the Hydra token-exchange round trip. 10s
// covers a slow first-boot DNS resolution + TLS handshake without
// letting a wedged Hydra block the entrypoint indefinitely.
const hydraTokenTimeout = 10 * time.Second

// logsDir is where clawkerd writes its rotated log file. Co-located
// with the entrypoint's stdout/stderr capture target (/var/log/clawker/)
// so an operator triaging issues finds bootstrap-time stderr writes
// AND structured log events under one directory. The directory is
// created by the entrypoint before clawkerd launches; clawkerd itself
// runs as root inside the container (the entrypoint backgrounds it
// before privilege drop), so 0o755 is fine.
const logsDir = "/var/log/clawker"

// logFilename is the rotated log file's basename — distinct from
// clawker.log on the host so an operator triaging issues can tell at a
// glance which side wrote which entries.
const logFilename = "clawkerd.log"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	// Logger init has to land BEFORE run() so every subsequent event
	// can flow through it. If the file-writer init itself fails we have
	// no other channel to surface the bootstrap problem on, so write to
	// stderr and exit non-zero — that's the only stderr write in the
	// whole daemon. No `defer` on the cleanup paths because os.Exit
	// skips deferred funcs; ordering is explicit instead.
	log, err := logger.New(logger.Options{
		LogsDir:  logsDir,
		Filename: logFilename,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawkerd: logger init failed: %v\n", err)
		stop()
		os.Exit(1)
	}

	exitCode := 0
	if err := run(ctx, log); err != nil {
		log.Error().Err(err).Str("event", "shutdown").Msg("clawkerd exiting with error")
		exitCode = 1
	} else {
		log.Info().Str("event", "shutdown").Msg("clawkerd exiting cleanly")
	}
	_ = log.Close()
	stop()
	os.Exit(exitCode)
}

func run(ctx context.Context, log *logger.Logger) error {
	hydraURL := os.Getenv(consts.EnvClawkerdHydraURL)
	agentAddr := os.Getenv(consts.EnvClawkerdAgentAddr)
	agentName := os.Getenv(consts.EnvAgent)
	// CLAWKER_PROJECT is allowed to be empty — empty matches the
	// 2-segment naming case (docker.ContainerName behavior) where the
	// canonical CN is "clawker.<agent>" and the slot key folds an empty
	// project string. Required env validation below only checks the
	// three load-bearing fields; project is read but not required.
	project := os.Getenv(consts.EnvProject)
	if hydraURL == "" || agentAddr == "" || agentName == "" {
		return fmt.Errorf("required env not set: %s, %s, %s",
			consts.EnvClawkerdHydraURL, consts.EnvClawkerdAgentAddr, consts.EnvAgent)
	}

	// Bind agent + project on every subsequent log line so a multi-
	// container clawkerd log (if/when shared on disk via volume mount)
	// stays trivially filterable. Match the field names AdminService
	// uses on its side so a CP/agent log correlation grep on `agent=`
	// joins both halves of the handshake.
	log = log.With("agent", agentName, "project", project)
	log.Info().
		Str("event", "boot").
		Str("bootstrap_dir", consts.BootstrapDir).
		Str("hydra_url", hydraURL).
		Str("agent_addr", agentAddr).
		Msg("clawkerd starting")

	boot, err := readBootstrap(consts.BootstrapDir)
	if err != nil {
		log.Error().Err(err).Str("event", "bootstrap_read_failed").Msg("read bootstrap")
		return fmt.Errorf("read bootstrap: %w", err)
	}

	// Start the ClawkerdService listener BEFORE registering with CP.
	// CP may dial concurrently with the registration call, so the
	// listener must be ready first. Listener uses the same per-agent
	// leaf cert as the outbound dial; the security boundary is the
	// CN-pin on incoming peer certs (only consts.ContainerCP allowed).
	clawkerdSrv, err := startClawkerdListener(boot, log)
	if err != nil {
		log.Error().Err(err).Str("event", "clawkerd_listener_start_failed").Msg("start clawkerd listener")
		return fmt.Errorf("start clawkerd listener: %w", err)
	}
	defer func() {
		log.Info().Str("event", "clawkerd_listener_stopping").Msg("graceful stop")
		clawkerdSrv.GracefulStop()
		log.Info().Str("event", "clawkerd_listener_stopped").Msg("listener torn down")
	}()

	tokenURL := strings.TrimRight(hydraURL, "/") + "/oauth2/token"
	tokenTLS, err := buildTokenTLSConfig(boot.CACertPEM)
	if err != nil {
		log.Error().Err(err).Str("event", "token_tls_build_failed").Msg("token TLS config")
		return fmt.Errorf("token TLS config: %w", err)
	}

	log.Info().Str("event", "token_exchange_attempt").Str("url", tokenURL).Msg("posting client_assertion to Hydra")
	tokenCtx, tokenCancel := context.WithTimeout(ctx, hydraTokenTimeout)
	defer tokenCancel()
	token, err := exchangeAssertion(tokenCtx, tokenURL, boot.Assertion, tokenTLS)
	if err != nil {
		log.Error().Err(err).Str("event", "token_exchange_failed").Msg("hydra token exchange")
		return fmt.Errorf("hydra token exchange: %w", err)
	}
	log.Info().Str("event", "token_acquired").Msg("Hydra issued access token")

	dialTLS, err := buildDialTLSConfig(boot.CertPEM, boot.KeyPEM, boot.CACertPEM)
	if err != nil {
		log.Error().Err(err).Str("event", "dial_tls_build_failed").Msg("dial TLS config")
		return fmt.Errorf("dial TLS config: %w", err)
	}

	log.Info().Str("event", "connect_dial").Str("addr", agentAddr).Msg("dialing CP agent listener")
	conn, err := grpc.NewClient(
		agentAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(dialTLS)),
		// PerRPCCredentials covers BOTH unary and streaming RPCs. The
		// previous unary-only interceptor silently skipped Connect
		// (server-streaming) — CP would reject every announce attempt
		// with codes.Unauthenticated before the agent saw Welcome.
		grpc.WithPerRPCCredentials(newBearerCreds(token)),
	)
	if err != nil {
		log.Error().Err(err).Str("event", "connect_dial_failed").Msg("dial CP agent listener")
		return fmt.Errorf("dial CP agent listener: %w", err)
	}
	defer func() {
		// Connection close is informational at exit but useful for
		// debugging stuck FD leaks across rapid container churn. Log at
		// debug — operators triaging shutdown rarely need to see it,
		// but a regression that leaks conns shows up here.
		if cerr := conn.Close(); cerr != nil {
			log.Error().Err(cerr).Str("event", "connection_close_failed").Msg("closing CP agent connection")
		} else {
			log.Debug().Str("event", "connection_closed").Msg("CP agent connection closed")
		}
	}()

	// Construct the AgentService client even though clawkerd no longer
	// calls Register at boot in this branch — agentClient and the dial
	// machinery above (Hydra token exchange, mTLS conn, bearer creds)
	// are the foundation for the agent→CP RPCs that land in upcoming
	// branches. Keeping the wiring live means the next RPC just needs
	// its own call site, not a re-derivation of the auth chain.
	agentClient := agentv1.NewAgentServiceClient(conn)
	_ = agentClient

	log.Info().Str("event", "daemon_idle").Msg("entering daemon idle loop; CP may dial Session at any time")

	// clawkerd is a DAEMON. Its lifetime is the container's lifetime,
	// bounded only by SIGTERM (ctx cancel). After Register succeeds
	// the only outstanding responsibility is the :7700 ClawkerdService
	// listener (already serving) where CP dials in to dispatch
	// commands. CP→clawkerd connection breaks are logged from the
	// listener side but do not kill the daemon.
	<-ctx.Done()
	log.Info().Str("event", "shutdown_signal_received").Msg("SIGTERM received; tearing down clawkerd")
	return nil
}

// bootstrap mirrors the CLI's per-agent registration material on disk.
type bootstrap struct {
	CertPEM, KeyPEM, CACertPEM []byte
	Assertion                  string
	Verifier                   string
}

// readBootstrap reads the five bootstrap files from dir. Missing files
// fail loudly — a partial boot is a security regression (e.g. cert
// missing while verifier present would let clawkerd register without
// the cert pinning that defends against tmpfs swap).
func readBootstrap(dir string) (*bootstrap, error) {
	read := func(name string) ([]byte, error) {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("%s is empty", name)
		}
		return data, nil
	}

	cert, err := read(consts.BootstrapCertFile)
	if err != nil {
		return nil, err
	}
	key, err := read(consts.BootstrapKeyFile)
	if err != nil {
		return nil, err
	}
	ca, err := read(consts.BootstrapCAFile)
	if err != nil {
		return nil, err
	}
	assertion, err := read(consts.BootstrapAssertionFile)
	if err != nil {
		return nil, err
	}
	verifier, err := read(consts.BootstrapVerifierFile)
	if err != nil {
		return nil, err
	}

	return &bootstrap{
		CertPEM:   cert,
		KeyPEM:    key,
		CACertPEM: ca,
		Assertion: strings.TrimSpace(string(assertion)),
		Verifier:  strings.TrimSpace(string(verifier)),
	}, nil
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
// shot — the bearer is consumed via PerRPCCredentials on every
// outgoing RPC during the lifetime of the gRPC connection. Token
// refresh lands with the cp-restart-resilience initiative alongside
// reconnect-with-backoff.
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
	// Hydra always returns "Bearer" today, but defend against a future
	// Hydra upgrade or misconfig that returns "DPoP" or some other
	// type — clawkerd would happily attach the token as
	// `authorization: Bearer <token>` and CP would reject mid-stream
	// with an opaque codes.Unauthenticated. Fail early with a clear
	// error so the operator sees the actual problem.
	if out.TokenType != "" && !strings.EqualFold(out.TokenType, "Bearer") {
		return "", fmt.Errorf("hydra returned unexpected token_type %q (expected Bearer)", out.TokenType)
	}
	return out.AccessToken, nil
}
