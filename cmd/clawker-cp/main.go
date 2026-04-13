// clawker-cp is the containerized clawker control plane binary.
//
// It runs as the main process in the CP container, supervising Hydra,
// Oathkeeper, Kratos as subprocesses. It loads eBPF programs, serves a
// gRPC AdminService with Hydra token introspection, and reports
// readiness on /healthz.
//
// Oathkeeper runs as a subprocess for future webui HTTP auth. gRPC auth
// (CLI + agents) uses direct Hydra introspection — no Ory Go imports.
//
// Startup sequence (any failure = crash with diagnostic error):
//  1. Start Kratos subprocess
//  2. Start Hydra subprocess (in-memory store, admin on 127.0.0.1:4445)
//  3. Wait for both healthy
//  4. Read CLI JWK from bind-mounted file
//  5. Register CLI client via Hydra admin API
//  6. Start Oathkeeper HTTP proxy subprocess (for future webui)
//  7. Load eBPF programs
//  8. Start gRPC admin API with Hydra token introspection
//  9. Report healthy on /healthz
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultShutdownWait = 5 * time.Second
	healthCheckInterval = 200 * time.Millisecond
	healthCheckTimeout  = 30 * time.Second
)

func main() {
	caCertPath := flag.String("tls-ca", "/etc/clawker/tls/ca.pem", "CLI CA certificate")
	serverCertPath := flag.String("tls-cert", "/etc/clawker/tls/server.pem", "TLS server certificate")
	serverKeyPath := flag.String("tls-key", "/etc/clawker/tls/server.key", "TLS server key")
	jwkPath := flag.String("jwk", "/etc/clawker/cli/signing-jwk.json", "CLI signing JWK (bind-mounted)")
	logDir := flag.String("log-dir", "/var/log/clawker", "directory for persistent audit logs")
	flag.Parse()

	if err := run(*caCertPath, *serverCertPath, *serverKeyPath, *jwkPath, *logDir); err != nil {
		fmt.Fprintf(os.Stderr, "clawker-cp: %v\n", err)
		os.Exit(1)
	}
}

func run(caCertPath, serverCertPath, serverKeyPath, jwkPath, logDir string) error {
	log, err := logger.New(logger.Options{
		LogsDir:  logDir,
		Filename: "clawker-cp.log",
	})
	if err != nil {
		// Fall back to stderr-only if log dir isn't writable.
		log = logger.NewWriter(os.Stderr)
		fmt.Fprintf(os.Stderr, "clawker-cp: warning: file logging unavailable (%v), using stderr only\n", err)
	}
	log = log.With("component", "clawker-cp")
	defer log.Close()
	log.Info().Msg("starting")

	// Load config from the mounted config dir (CLAWKER_CONFIG_DIR set by
	// the container env). All port values come from settings.ControlPlane.
	cfg, err := config.NewConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cp := cfg.Settings().ControlPlane

	subMgr := controlplane.NewSubprocessManager(log)
	orchestrator := controlplane.NewCPStartupOrchestrator()

	// --- Step 0: Write Ory config files ---
	if err := controlplane.WriteOryConfigs(cp); err != nil {
		return fmt.Errorf("step 0 (write ory configs): %w", err)
	}
	log.Info().Msg("Ory config files written")

	// --- Step 1: Start Kratos ---
	kratosCmd := exec.Command("kratos", "serve",
		"--config", "/etc/clawker/kratos.yaml",
	)
	if err := subMgr.Start("kratos", kratosCmd); err != nil {
		return fmt.Errorf("step 1 (kratos): %w", err)
	}

	// --- Step 2: Start Hydra ---
	hydraCmd := exec.Command("hydra", "serve", "all",
		"--config", "/etc/clawker/hydra.yaml",
		"--sqa-opt-out",
		"--dev",
	)
	if err := subMgr.Start("hydra", hydraCmd); err != nil {
		return fmt.Errorf("step 2 (hydra): %w", err)
	}

	// --- Step 3: Wait for both healthy ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build CLI CA cert pool — used for health checks, Hydra introspection,
	// client registration, and mTLS client cert verification.
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("step 3 (read CA cert): %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCertPEM) {
		return fmt.Errorf("step 3: failed to parse CA cert")
	}
	caTLS := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS13,
	}

	if err := subMgr.WaitHealthy(ctx, "kratos", controlplane.HealthCheck{
		URL: fmt.Sprintf("https://127.0.0.1:%d/health/alive", cp.KratosPublicPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: caTLS,
	}); err != nil {
		return fmt.Errorf("step 3 (kratos health): %w", err)
	}
	if err := subMgr.WaitHealthy(ctx, "hydra", controlplane.HealthCheck{
		URL: fmt.Sprintf("https://127.0.0.1:%d/health/alive", cp.HydraPublicPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: caTLS,
	}); err != nil {
		return fmt.Errorf("step 3 (hydra health): %w", err)
	}

	// --- Step 3b: Wait for Hydra admin port ---
	// The public health check (step 3) confirms the public listener is ready,
	// but client registration goes to the admin port — a separate listener
	// that may take longer under resource pressure. Wait for it explicitly.
	if err := subMgr.WaitHealthy(ctx, "hydra", controlplane.HealthCheck{
		URL: fmt.Sprintf("https://127.0.0.1:%d/health/alive", cp.HydraAdminPort), Interval: healthCheckInterval, Timeout: healthCheckTimeout,
		TLS: caTLS,
	}); err != nil {
		return fmt.Errorf("step 3b (hydra admin health): %w", err)
	}

	// Configure aggregate health probes. The /healthz endpoint will actively
	// probe ALL service ports — it only returns 200 when every one responds.
	orchestrator.SetServiceProbes(cp, caTLS)

	// --- Step 4: Read CLI JWK ---
	jwkData, err := os.ReadFile(jwkPath)
	if err != nil {
		return fmt.Errorf("step 4 (read JWK %s): %w", jwkPath, err)
	}
	log.Info().Str("jwk_path", jwkPath).Msg("CLI JWK loaded")

	// --- Step 5: Register CLI client with Hydra ---
	hydraAdminURL := fmt.Sprintf("https://127.0.0.1:%d", cp.HydraAdminPort)
	if err := controlplane.RegisterCLIClient(hydraAdminURL, jwkData, caTLS); err != nil {
		return fmt.Errorf("step 5 (register CLI client): %w", err)
	}
	log.Info().Msg("CLI client registered with Hydra")

	// --- Step 6: Start Oathkeeper ---
	// Oathkeeper runs as an HTTP reverse proxy for future webui auth.
	// gRPC auth (CLI + agents) bypasses Oathkeeper entirely — it uses
	// direct Hydra token introspection via AuthInterceptor.
	oathkeeperCmd := exec.Command("oathkeeper", "serve",
		"--config", "/etc/clawker/oathkeeper.yaml",
	)
	if err := subMgr.Start("oathkeeper", oathkeeperCmd); err != nil {
		return fmt.Errorf("step 6 (oathkeeper): %w", err)
	}

	// --- Step 7: Load eBPF programs ---
	ebpfMgr := ebpf.NewManager(log)
	if err := ebpfMgr.Load(); err != nil {
		return fmt.Errorf("step 7 (ebpf load): %w", err)
	}
	defer func() {
		if err := ebpfMgr.Close(); err != nil {
			log.Warn().Err(err).Msg("ebpf close error")
		}
	}()
	log.Info().Msg("eBPF programs loaded")

	// --- Step 8: Start gRPC admin API ---
	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		return fmt.Errorf("step 8 (load server cert): %w", err)
	}

	// mTLS: require client certificates signed by the CLI CA.
	// caCertPool already contains the CA cert (parsed at step 3).
	// Authorization is still via OAuth2 bearer tokens — mTLS authenticates
	// the transport channel.
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13,
	}

	// Auth interceptor: validates bearer tokens via Hydra introspection.
	hydraIntrospectURL := fmt.Sprintf("https://127.0.0.1:%d/admin/oauth2/introspect", cp.HydraAdminPort)
	introspector := controlplane.NewHydraIntrospector(hydraIntrospectURL, caTLS)
	authInterceptor := controlplane.NewAuthInterceptor(introspector, controlplane.AdminMethodScopes(), log)

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor()),
	)

	handler := controlplane.NewAdminHandler(ebpfMgr, log)
	adminv1.RegisterAdminServiceServer(grpcServer, handler)

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(cp.AdminPort))
	if err != nil {
		return fmt.Errorf("step 8 (grpc listen): %w", err)
	}

	go func() {
		log.Info().Int("port", cp.AdminPort).Msg("gRPC admin API serving")
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Error().Err(err).Msg("gRPC serve error")
		}
	}()

	// --- Step 9: healthz ---
	orchestrator.SetReady()

	healthMux := http.NewServeMux()
	healthMux.Handle("/healthz", orchestrator.HealthzHandler())
	healthServer := &http.Server{
		Addr:    "0.0.0.0:" + strconv.Itoa(cp.HealthPort),
		Handler: healthMux,
	}
	go func() {
		log.Info().Int("port", cp.HealthPort).Msg("healthz serving")
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("healthz serve error")
		}
	}()

	log.Info().Msg("clawker-cp ready")

	// --- Block on signal or subprocess crash ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		log.Info().Stringer("signal", sig).Msg("shutdown signal received")
	case err := <-subMgr.CrashChan():
		log.Error().Err(err).Msg("subprocess crashed — shutting down")
		return err
	}

	// --- Graceful shutdown (reverse order) ---
	shutdownDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		grpcServer.GracefulStop()
	}()
	go func() {
		wg.Wait()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(defaultShutdownWait):
		log.Warn().Msg("gRPC graceful stop timed out, forcing")
		grpcServer.Stop()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownWait)
	defer shutdownCancel()
	_ = healthServer.Shutdown(shutdownCtx)

	subMgr.Shutdown(defaultShutdownWait)
	log.Info().Msg("clawker-cp stopped")
	return nil
}
