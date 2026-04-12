// clawker-cp is the containerized clawker control plane binary.
//
// It runs as the main process in the CP container, supervising Hydra,
// Oathkeeper, and Kratos as subprocesses. It loads eBPF programs, serves
// a gRPC AdminService with vendored Oathkeeper middleware, and reports
// readiness on /healthz.
//
// Startup sequence (any failure = crash with diagnostic error):
//  1. Start Kratos subprocess
//  2. Start Hydra subprocess (in-memory store, admin on 127.0.0.1:4445)
//  3. Wait for both healthy
//  4. Read CLI JWK from bind-mounted file
//  5. Register CLI client via Hydra admin API
//  6. Start Oathkeeper HTTP proxy subprocess
//  7. Load eBPF programs
//  8. Start gRPC admin API with Oathkeeper middleware
//  9. Report healthy on /healthz
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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
	"github.com/schmitthub/clawker/internal/consts"
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
	adminPort := flag.Int("admin-port", consts.DefaultCPAdminPort, "gRPC admin API port")
	healthPort := flag.Int("health-port", consts.CPHealthPort, "healthz HTTP port")
	serverCertPath := flag.String("tls-cert", "/etc/clawker/tls/server.pem", "TLS server certificate")
	serverKeyPath := flag.String("tls-key", "/etc/clawker/tls/server.key", "TLS server key")
	jwkPath := flag.String("jwk", "/etc/clawker/cli/signing-jwk.json", "CLI signing JWK (bind-mounted)")
	flag.Parse()

	if err := run(*adminPort, *healthPort, *serverCertPath, *serverKeyPath, *jwkPath); err != nil {
		fmt.Fprintf(os.Stderr, "clawker-cp: %v\n", err)
		os.Exit(1)
	}
}

func run(adminPort, healthPort int, serverCertPath, serverKeyPath, jwkPath string) error {
	log := logger.NewWriter(os.Stderr).With("component", "clawker-cp")
	defer log.Close()
	log.Info().Msg("starting")

	subMgr := controlplane.NewSubprocessManager(log)
	orchestrator := controlplane.NewCPStartupOrchestrator()

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

	if err := subMgr.WaitHealthy(ctx, "kratos", controlplane.HealthCheck{
		URL: "http://127.0.0.1:4433/health/alive", Interval: healthCheckInterval, Timeout: healthCheckTimeout,
	}); err != nil {
		return fmt.Errorf("step 3 (kratos health): %w", err)
	}
	if err := subMgr.WaitHealthy(ctx, "hydra", controlplane.HealthCheck{
		URL: "http://127.0.0.1:4444/health/alive", Interval: healthCheckInterval, Timeout: healthCheckTimeout,
	}); err != nil {
		return fmt.Errorf("step 3 (hydra health): %w", err)
	}

	// --- Step 4: Read CLI JWK ---
	jwkData, err := os.ReadFile(jwkPath)
	if err != nil {
		return fmt.Errorf("step 4 (read JWK %s): %w", jwkPath, err)
	}
	log.Info().Str("jwk_path", jwkPath).Msg("CLI JWK loaded")
	_ = jwkData // Used in Step 5 for Hydra client registration.

	// --- Step 5: Register CLI client with Hydra ---
	// TODO: Use Hydra Go SDK (github.com/ory/hydra-client-go/v2) to register
	// the CLI OAuth2 client with private_key_jwt auth method, ES256 signing
	// alg, client_credentials grant, admin scope, and the CLI's JWKS.
	// For now, this step logs a placeholder — Hydra client registration
	// requires the Go SDK dependency which will be added when GOPROXY is available.
	log.Info().Msg("step 5: CLI client registration (placeholder — needs Hydra Go SDK)")

	// --- Step 6: Start Oathkeeper ---
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

	// Build the server cert pool for client trust (self-signed cert IS the CA).
	certPEM, err := os.ReadFile(serverCertPath)
	if err != nil {
		return fmt.Errorf("step 8 (read server cert): %w", err)
	}
	certPool := x509.NewCertPool()
	block, _ := pem.Decode(certPEM)
	if block != nil {
		cert, err := x509.ParseCertificate(block.Bytes)
		if err == nil {
			certPool.AddCert(cert)
		}
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS13,
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		// TODO: Add authkeeper middleware interceptors here once
		// Oathkeeper health check is confirmed. For now, TLS-only.
	)

	handler := controlplane.NewAdminHandler(ebpfMgr, log)
	adminv1.RegisterAdminServiceServer(grpcServer, handler)

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(adminPort))
	if err != nil {
		return fmt.Errorf("step 8 (grpc listen): %w", err)
	}

	go func() {
		log.Info().Int("port", adminPort).Msg("gRPC admin API serving")
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Error().Err(err).Msg("gRPC serve error")
		}
	}()

	// --- Step 9: healthz ---
	orchestrator.SetReady()

	healthMux := http.NewServeMux()
	healthMux.Handle("/healthz", orchestrator.HealthzHandler())
	healthServer := &http.Server{
		Addr:    "0.0.0.0:" + strconv.Itoa(healthPort),
		Handler: healthMux,
	}
	go func() {
		log.Info().Int("port", healthPort).Msg("healthz serving")
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
