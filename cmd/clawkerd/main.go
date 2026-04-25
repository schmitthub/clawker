// Command clawkerd is the per-container agent daemon. It runs as PID 0
// in every clawker-managed container, completes the registration
// handshake with the control plane on the agent gRPC listener, and then
// idles until SIGTERM. Branch 4 ships only the Register call — no
// heartbeat, no command receiver — because the CP knows liveness via
// Docker events + mTLS connection state.
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
//  5. Register({agent_name, code_verifier}). Delete the verifier
//     immediately on success (single-use; PKCE consumption IS the
//     replay defense).
//  6. Idle on the open connection until ctx is cancelled by SIGTERM.
//     CP detects clawkerd death via gRPC connection drop + dockerevents.
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

// registerTimeout bounds the AgentService.Register call. Should be well
// under the slot TTL so a wedged Register surfaces as a clear failure
// rather than an opaque slot expiry.
const registerTimeout = 30 * time.Second

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
	agentName := os.Getenv(consts.EnvClawkerdAgentName)
	if hydraURL == "" || agentAddr == "" || agentName == "" {
		return fmt.Errorf("required env not set: %s, %s, %s",
			consts.EnvClawkerdHydraURL, consts.EnvClawkerdAgentAddr, consts.EnvClawkerdAgentName)
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
		grpc.WithChainUnaryInterceptor(bearerInterceptor(token)),
	)
	if err != nil {
		return fmt.Errorf("dial CP agent listener: %w", err)
	}
	defer conn.Close()

	agentClient := agentv1.NewAgentServiceClient(conn)

	regCtx, regCancel := context.WithTimeout(ctx, registerTimeout)
	defer regCancel()
	if _, err := agentClient.Register(regCtx, &agentv1.RegisterRequest{
		AgentName:    agentName,
		CodeVerifier: boot.Verifier,
	}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	// Verifier is single-use — delete it now so a stolen filesystem
	// snapshot of the running container can't replay registration
	// against another agent. Assertion+cert+key+CA stay until the
	// container dies; clawkerd needs them for any future redial in
	// the (currently improbable) event the connection drops.
	verifierPath := filepath.Join(consts.BootstrapDir, consts.BootstrapVerifierFile)
	if rmErr := os.Remove(verifierPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "clawkerd: warning: removing verifier: %v\n", rmErr)
	}

	// Idle until SIGTERM. The mTLS connection holds open and signals
	// liveness to the CP — no heartbeat needed. dockerevents owns the
	// other side of the liveness signal (container die → registry
	// evict).
	<-ctx.Done()
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
// shot — Branch 4 only calls Register, so no refresh loop is wired.
// Future per-agent RPCs will need a token source; that lands with
// their consumer.
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
