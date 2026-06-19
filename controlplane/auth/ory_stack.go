package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/schmitthub/clawker/controlplane/subprocess"
	authmat "github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// healthCheckInterval and healthCheckTimeout bound the per-service readiness
// polls during the Ory bringup. They mirror the values the orchestrator used
// inline before this stack was extracted.
const (
	healthCheckInterval = 200 * time.Millisecond
	healthCheckTimeout  = 30 * time.Second
)

// OryStack owns the control plane's Ory authentication choreography: Kratos
// (identity), Hydra (OAuth2 token issuance + introspection), and Oathkeeper
// (HTTP reverse-proxy auth for the future webui). It writes the Ory config
// files, starts each daemon as a managed subprocess, waits for them to become
// healthy, and registers the CLI and agent OAuth2 clients with Hydra.
//
// The CA pool / TLS config built here (CATLS / CACertPool) is the SINGLE CA
// surface for the control plane — health checks, Hydra introspection, client
// registration, and gRPC mTLS client-cert verification all reuse it. The gRPC
// stack reuses these accessors rather than building a second pool.
//
// subMgr is orchestrator-owned: OryStack uses it to start/health-check the Ory
// daemons but never owns its lifecycle (shutdown/drain stay with the
// orchestrator).
//
// Every gate returns an error (fail-closed). OryStack never panics, os.Exit's,
// or log.Fatal's — the orchestrator decides what a startup-gate failure means.
type OryStack struct {
	cfg        config.Config
	subMgr     *subprocess.SubprocessManager
	caCertPath string
	jwkPath    string
	log        *logger.Logger
	caCertPool *x509.CertPool
	caTLS      *tls.Config
}

// NewOryStack constructs the Ory auth stack and builds the single CP CA pool /
// TLS config from caCertPath up front, so CATLS / CACertPool are available to
// the orchestrator (and the gRPC stack) before Start runs. It does NOT start
// any subprocess — call Start for that.
//
// Returns an error (never panics) if the CA cert cannot be read or parsed.
func NewOryStack(cfg config.Config, subMgr *subprocess.SubprocessManager, caCertPath, jwkPath string, log *logger.Logger) (*OryStack, error) {
	// Build CLI CA cert pool — used for health checks, Hydra introspection,
	// client registration, and mTLS client cert verification.
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA cert")
	}
	caTLS := &tls.Config{
		RootCAs:    caCertPool,
		ServerName: consts.ContainerCP,
		MinVersion: tls.VersionTLS13,
	}

	return &OryStack{
		cfg:        cfg,
		subMgr:     subMgr,
		caCertPath: caCertPath,
		jwkPath:    jwkPath,
		log:        log,
		caCertPool: caCertPool,
		caTLS:      caTLS,
	}, nil
}

// CATLS returns the single CP CA TLS config (RootCAs pinned to the CLI CA,
// ServerName the CP container name, TLS 1.3 floor). Reused by the orchestrator
// (aggregate health probes) and the gRPC stack — never build a second pool.
func (s *OryStack) CATLS() *tls.Config { return s.caTLS }

// CACertPool returns the single CP CA cert pool. Reused for mTLS client-cert
// verification by the gRPC stack.
func (s *OryStack) CACertPool() *x509.CertPool { return s.caCertPool }

// Start runs the Ory bringup choreography, fail-closed: ensure the Hydra
// secret, write the Ory configs, start Kratos + Hydra, wait for all three
// listeners (Kratos public, Hydra public, Hydra admin) to report healthy,
// register the CLI + agent OAuth2 clients, then start Oathkeeper. Any failure
// returns an error; the orchestrator treats this as a pre-SetReady startup
// gate.
func (s *OryStack) Start(ctx context.Context) error {
	cp := s.cfg.Settings().ControlPlane

	hydraSecret, err := authmat.EnsureHydraSecret()
	if err != nil {
		return fmt.Errorf("hydra secret: %w", err)
	}
	if err := WriteOryConfigs(cp, hydraSecret); err != nil {
		return fmt.Errorf("write ory configs: %w", err)
	}
	s.log.Info().Msg("Ory config files written")

	kratosCmd := exec.Command("kratos", "serve",
		"--config", consts.CPKratosConfigPath,
	)
	if err := s.subMgr.Start("kratos", kratosCmd); err != nil {
		return fmt.Errorf("kratos: %w", err)
	}

	hydraCmd := exec.Command("hydra", "serve", "all",
		"--config", consts.CPHydraConfigPath,
		"--sqa-opt-out",
		"--dev",
	)
	if err := s.subMgr.Start("hydra", hydraCmd); err != nil {
		return fmt.Errorf("hydra: %w", err)
	}

	if err := s.subMgr.WaitHealthy(ctx, "kratos", subprocess.HealthCheck{
		URL: fmt.Sprintf("https://"+consts.Localhost+":%d/health/alive", cp.KratosPublicPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: s.caTLS,
	}); err != nil {
		return fmt.Errorf("kratos health: %w", err)
	}
	if err := s.subMgr.WaitHealthy(ctx, "hydra", subprocess.HealthCheck{
		URL: fmt.Sprintf("https://"+consts.Localhost+":%d/health/alive", cp.HydraPublicPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: s.caTLS,
	}); err != nil {
		return fmt.Errorf("hydra health: %w", err)
	}

	// The public health check above confirms the public listener is ready,
	// but client registration goes to the admin port — a separate listener
	// that may take longer under resource pressure. Wait for it explicitly.
	if err := s.subMgr.WaitHealthy(ctx, "hydra", subprocess.HealthCheck{
		URL: fmt.Sprintf("https://"+consts.Localhost+":%d/health/alive", cp.HydraAdminPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: s.caTLS,
	}); err != nil {
		return fmt.Errorf("hydra admin health: %w", err)
	}

	jwkData, err := os.ReadFile(s.jwkPath)
	if err != nil {
		return fmt.Errorf("read JWK %s: %w", s.jwkPath, err)
	}
	s.log.Info().Str("jwk_path", s.jwkPath).Msg("CLI JWK loaded")

	// See RegisterAgentClient for why both clients share
	// one JWK with distinct client_id + scope.
	hydraAdminURL := fmt.Sprintf("https://"+consts.Localhost+":%d", cp.HydraAdminPort)
	if err := RegisterCLIClient(ctx, hydraAdminURL, jwkData, s.caTLS); err != nil {
		return fmt.Errorf("register CLI client: %w", err)
	}
	if err := RegisterAgentClient(ctx, hydraAdminURL, jwkData, s.caTLS); err != nil {
		return fmt.Errorf("register agent client: %w", err)
	}
	s.log.Info().Msg("CLI + agent clients registered with Hydra")

	// Oathkeeper runs as an HTTP reverse proxy for future webui auth.
	// gRPC (CLI + agents) bypasses Oathkeeper entirely — it uses
	// direct Hydra token introspection via AuthInterceptor.
	oathkeeperCmd := exec.Command("oathkeeper", "serve",
		"--config", consts.CPOathkeeperConfigPath,
	)
	if err := s.subMgr.Start("oathkeeper", oathkeeperCmd); err != nil {
		return fmt.Errorf("oathkeeper: %w", err)
	}

	return nil
}
