// Command clawkerd is the per-container agent daemon. It runs as a
// backgrounded child of the container entrypoint shell (started by
// internal/bundler/assets/entrypoint.sh after firewall healthz passes),
// opens the lifetime command channel with the control plane on the
// agent gRPC listener, and then drains commands until SIGTERM or the
// stream closes.
//
// Boot sequence:
//
//  1. Read bootstrap material delivered by the CLI to consts.BootstrapDir.
//     The five files (cert.pem, key.pem, ca.pem, assertion.jwt, verifier)
//     were tarred into the container's writable layer at announce time
//     and are root-only readable.
//  2. Resolve three env vars: Hydra public URL, CP agent listener
//     address on clawker-net, canonical agent name. Anything more is
//     deliberately not in the env — the daemon should not be able to
//     assert identity it didn't receive on a defended channel.
//  3. POST the CLI-signed client_assertion to Hydra → access token
//     bound to the clawker-agent client + agent:self:register scope.
//  4. mTLS-dial the CP agent listener with the per-agent leaf cert.
//     Bearer token attached on every RPC.
//  5. Connect({agent_name, code_verifier}) opens the server-streaming
//     command channel. The first message is Welcome — receipt implies
//     server-side auth fully succeeded, so the single-use verifier is
//     safe to delete only after Welcome lands.
//  6. Drain the stream until ctx is cancelled (SIGTERM) or the stream
//     closes (EOF on graceful CP shutdown, error on transport break).
//     CP detects clawkerd death via gRPC connection drop + dockerevents.
//     B5+ adds command-payload variants (ShellCommand, Stop, ReloadConfig)
//     to the oneof; today the loop only acknowledges Welcome and ignores
//     unknown variants forward-compatibly.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
)

// hydraTokenTimeout bounds the Hydra token-exchange round trip. 10s
// covers a slow first-boot DNS resolution + TLS handshake without
// letting a wedged Hydra block the entrypoint indefinitely.
const hydraTokenTimeout = 10 * time.Second

// welcomeTimeout bounds how long Connect waits for the first Welcome
// message after the stream opens. Should be well under the slot TTL so
// a wedged handshake surfaces as a clear failure rather than an opaque
// slot expiry. Once Welcome arrives, the stream lifetime is the agent's
// lifetime — no further timeout applies.
const welcomeTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	exitCode := 0
	if err := run(ctx); err != nil {
		// Stderr only — clawkerd's stdout is intentionally quiet so
		// container logs don't fill up with announce noise during
		// normal operation.
		fmt.Fprintf(os.Stderr, "clawkerd: %v\n", err)
		exitCode = 1
	}
	stop()
	os.Exit(exitCode)
}

func run(ctx context.Context) error {
	hydraURL := os.Getenv(consts.EnvClawkerdHydraURL)
	agentAddr := os.Getenv(consts.EnvClawkerdAgentAddr)
	agentName := os.Getenv(consts.EnvAgent)
	if hydraURL == "" || agentAddr == "" || agentName == "" {
		return fmt.Errorf("required env not set: %s, %s, %s",
			consts.EnvClawkerdHydraURL, consts.EnvClawkerdAgentAddr, consts.EnvAgent)
	}

	boot, err := readBootstrap(consts.BootstrapDir)
	if err != nil {
		return fmt.Errorf("read bootstrap: %w", err)
	}

	tokenURL := strings.TrimRight(hydraURL, "/") + "/oauth2/token"
	tokenTLS, err := buildTokenTLSConfig(boot.CACertPEM)
	if err != nil {
		return fmt.Errorf("token TLS config: %w", err)
	}

	tokenCtx, tokenCancel := context.WithTimeout(ctx, hydraTokenTimeout)
	defer tokenCancel()
	token, err := exchangeAssertion(tokenCtx, tokenURL, boot.Assertion, tokenTLS)
	if err != nil {
		return fmt.Errorf("hydra token exchange: %w", err)
	}

	dialTLS, err := buildDialTLSConfig(boot.CertPEM, boot.KeyPEM, boot.CACertPEM)
	if err != nil {
		return fmt.Errorf("dial TLS config: %w", err)
	}

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
		return fmt.Errorf("dial CP agent listener: %w", err)
	}
	defer func() {
		// Match the rest of clawkerd's logging style — stderr only, no
		// zerolog. Close failures are informational at exit but useful
		// for debugging stuck FD leaks across rapid container churn.
		if cerr := conn.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "clawkerd: warning: closing CP agent connection: %v\n", cerr)
		}
	}()

	agentClient := agentv1.NewAgentServiceClient(conn)

	// Connect opens the lifetime command channel. The Connect call
	// itself returns immediately with a stream wrapper; the auth
	// handshake (slot consume + cross-checks) materializes when we
	// Recv the first message. Use ctx (not a per-call timeout) so
	// SIGTERM tears the stream down cleanly via stream.Context().
	stream, err := agentClient.Connect(ctx, &agentv1.ConnectRequest{
		AgentName:    agentName,
		CodeVerifier: boot.Verifier,
	})
	if err != nil {
		return fmt.Errorf("connect to CP: %w", err)
	}

	// Bound the wait for Welcome separately from the stream's lifetime.
	// A welcomeCtx that hits its deadline cancels the underlying stream
	// via gRPC's per-RPC ctx; we don't propagate that into the lifetime
	// loop below.
	welcomeCtx, welcomeCancel := context.WithTimeout(ctx, welcomeTimeout)
	defer welcomeCancel()
	first, err := recvWithCtx(welcomeCtx, stream)
	if err != nil {
		// SIGTERM during the handshake is a clean teardown, not a
		// crash — exit zero so a restart-on-failure policy doesn't
		// retrigger. Mirrors the post-Welcome loop's discipline.
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("connect: recv welcome: %w", err)
	}
	if _, ok := first.Payload.(*agentv1.Command_Welcome); !ok {
		return fmt.Errorf("connect: expected Welcome as first message, got %T", first.Payload)
	}

	// Welcome received → server-side auth fully succeeded → safe to
	// delete the single-use verifier. A stolen filesystem snapshot of
	// the running container now cannot replay registration against
	// another agent. Assertion + cert + key + CA stay until the
	// container dies (needed for any future redial in the CP-restart-
	// resilience initiative — see cp-initiative-cp-restart-resilience).
	verifierPath := filepath.Join(consts.BootstrapDir, consts.BootstrapVerifierFile)
	if rmErr := os.Remove(verifierPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "clawkerd: warning: removing verifier: %v\n", rmErr)
	}
	// (B5+ uses first.GetWelcome().GetConfig() to init logger/OTEL/etc.
	// from the CP-delivered ClawkerdConfiguration. Empty placeholder today.)

	// Drain the stream for the agent's lifetime. EOF means CP closed
	// cleanly (graceful shutdown / drain-to-zero); a non-EOF error
	// means transport broke or the CP rejected mid-stream — either
	// way clawkerd exits and the container's restart policy decides
	// whether to retry.
	for {
		cmd, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil // SIGTERM-initiated teardown is not an error
			}
			return fmt.Errorf("connect stream: %w", err)
		}
		switch cmd.Payload.(type) {
		case *agentv1.Command_Welcome:
			fmt.Fprintln(os.Stderr, "clawkerd: warning: received unexpected second Welcome — ignoring")
		default:
			// Forward-compat: B5+ adds payload variants. Unknown types
			// surface in stderr at info level so an operator can see
			// what landed; ignoring them is safe because the proto
			// oneof reserves tag space and the daemon's behavior is
			// not gated on command receipt.
			fmt.Fprintf(os.Stderr, "clawkerd: info: ignoring unknown command payload %T\n", cmd.Payload)
		}
	}
}

// recvWithCtx wraps stream.Recv() so a tighter inner deadline can fire
// independently of the outer RPC ctx. stream.Recv() honors only the
// ctx passed to Connect (the agent's lifetime ctx); we want a shorter
// welcomeTimeout for the FIRST receive without truncating the rest of
// the stream's lifetime.
//
// Goroutine lifecycle: on ctx cancel, this returns ctx.Err() while the
// spawned goroutine remains blocked on stream.Recv(). The buffered
// channel (capacity 1) lets the goroutine send-and-exit cleanly when
// the stream eventually errors out — which it will when run() returns
// and the deferred conn.Close() fires. Single-shot-on-error-path:
// callers that don't tear down the conn after a ctx-cancel return
// would leak the goroutine.
func recvWithCtx(ctx context.Context, stream agentv1.AgentService_ConnectClient) (*agentv1.Command, error) {
	type result struct {
		cmd *agentv1.Command
		err error
	}
	ch := make(chan result, 1)
	go func() {
		cmd, err := stream.Recv()
		ch <- result{cmd, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.cmd, r.err
	}
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
	return out.AccessToken, nil
}
