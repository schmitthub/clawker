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
//     6b. Initialize Docker client (container state verification)
//  7. Load eBPF programs
//  8. Start gRPC admin API with Hydra token introspection
//  9. Report healthy on /healthz
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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

	cerrdefs "github.com/containerd/errdefs"
	mobyclient "github.com/moby/moby/client"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// containerResolverFromDocker returns a firewall.ContainerResolver that
// resolves a container reference to its canonical ID + BPF cgroup path
// using the given cgroup driver. NotFound comes back as (cid, "", false,
// nil) so the Handler can distinguish "container gone" from "Docker
// unreachable". When the ref is itself a canonical 64-hex ID we preserve
// it as the cid even on NotFound so the Handler's stored-cgroup_id
// fallback in FirewallDisable still has a key to look up.
func containerResolverFromDocker(dc *docker.Client, cgroupDriver string) fwhandler.ContainerResolver {
	return func(ctx context.Context, ref string) (string, string, bool, error) {
		cid, err := fwhandler.ResolveContainerID(ctx, dc, ref)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				canonical := ""
				if fwhandler.IsCanonicalContainerID(ref) {
					canonical = ref
				}
				return canonical, "", false, nil
			}
			return "", "", false, err
		}
		return cid, fwhandler.EBPFCgroupPath(cgroupDriver, cid), true, nil
	}
}

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
		fmt.Fprintf(os.Stderr, "%s: %v\n", consts.ContainerCP, err)
		os.Exit(1)
	}
}

func run(caCertPath, serverCertPath, serverKeyPath, jwkPath, logDir string) error {
	log, err := logger.New(logger.Options{
		LogsDir:  logDir,
		Filename: consts.ControlPlaneLogFile,
	})
	if err != nil {
		// Fall back to stderr-only if log dir isn't writable.
		log = logger.NewWriter(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s: warning: file logging unavailable (%v), using stderr only\n", consts.ContainerCP, err)
	}
	log = log.With("component", consts.ContainerCP)
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
	hydraSecret, err := auth.EnsureHydraSecret()
	if err != nil {
		return fmt.Errorf("step 0 (hydra secret): %w", err)
	}
	if err := controlplane.WriteOryConfigs(cp, hydraSecret); err != nil {
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
		ServerName: consts.ContainerCP,
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
	if err := controlplane.RegisterCLIClient(ctx, hydraAdminURL, jwkData, caTLS); err != nil {
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

	// Step 6b: Docker client. Used by the container resolver (bypass
	// dead-man timer), the firewall stack (Envoy + CoreDNS sibling
	// containers over DooD), and the AgentWatcher poll loop.
	dockerCli, err := docker.NewClient(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("step 6b (docker client): %w", err)
	}
	defer dockerCli.Close()

	// Query cgroup driver once at startup and cache on the resolver. BPF
	// cgroup paths come from firewall.EBPFCgroupPath in the firewall
	// subpackage — the single source of truth for the systemd/cgroupfs
	// path formats.
	cgroupDriver, err := fwhandler.DetectCgroupDriver(ctx, dockerCli)
	if err != nil {
		return fmt.Errorf("step 6b (cgroup driver): %w", err)
	}
	log.Info().Str("cgroup_driver", cgroupDriver).Msg("Docker cgroup driver detected")

	containerResolver := containerResolverFromDocker(dockerCli, cgroupDriver)

	// Step 6c: Firewall stack handle. Host bootstrap owns EnsureRunning;
	// the drain-to-zero path below owns Stop.
	rulesStore, err := fwhandler.NewRulesStore(cfg)
	if err != nil {
		return fmt.Errorf("step 6c (rules store): %w", err)
	}
	stack := fwhandler.NewStack(dockerCli, cfg, log, rulesStore)

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

	// Step 7b: Defensive startup cleanup (INV-B2-013).
	// Load() already cleans up pinned link files for dead cgroups. A
	// mirror pass for bypass_map is needed because cgroup IDs are
	// reusable across container generations — a leftover bypass entry
	// from a crashed previous CP could grant a fresh unrelated container
	// unrestricted egress.
	cleared, err := ebpfMgr.CleanupStaleBypass()
	if err != nil {
		return fmt.Errorf("step 7b (defensive bypass cleanup): %w", err)
	}
	if cleared > 0 {
		log.Info().Int("cleared", cleared).Msg("defensive startup: cleared stale bypass_map entries")
	}

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
	authInterceptor := controlplane.NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log)

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor()),
	)

	handler := fwhandler.NewHandler(fwhandler.HandlerDeps{
		EBPF:     ebpfMgr,
		Stack:    stack,
		Store:    rulesStore,
		Cfg:      cfg,
		Resolver: containerResolver,
		Log:      log,
	})
	adminv1.RegisterAdminServiceServer(grpcServer, controlplane.NewAdminServer(handler))

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(cp.AdminPort))
	if err != nil {
		return fmt.Errorf("step 8 (grpc listen): %w", err)
	}

	serveFailed := make(chan error, 2)

	go func() {
		log.Info().Int("port", cp.AdminPort).Msg("gRPC admin API serving")
		if err := grpcServer.Serve(grpcLis); err != nil {
			serveFailed <- fmt.Errorf("gRPC serve: %w", err)
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
			serveFailed <- fmt.Errorf("healthz serve: %w", err)
		}
	}()

	// Step 9b: AgentWatcher self-shutdown.
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()

	listAgents := func(ctx context.Context) (int, error) {
		filter := mobyclient.Filters{}.
			Add("label", cfg.LabelManaged()+"="+cfg.ManagedLabelValue()).
			Add("label", cfg.LabelPurpose()+"="+cfg.PurposeAgent())
		result, err := dockerCli.APIClient.ContainerList(ctx, mobyclient.ContainerListOptions{Filters: filter})
		if err != nil {
			return 0, err
		}
		return len(result.Items), nil
	}

	drainCallback := func(ctx context.Context) error {
		// Strict ordering (INV-B2-007):
		//  1. Cancel in-flight bypass timers so no scheduled Enable fires
		//     against maps that are about to be emptied.
		//  2. grpcServer.GracefulStop refuses new RPCs and waits for
		//     in-flight ones to finish — without this, a late Install
		//     could write container_map/bypass_map state after FlushAll
		//     and the next CP instance would see a ghost entry, defeating
		//     INV-B2-013.
		//  3. Stop the firewall stack (Envoy + CoreDNS).
		//  4. Flush per-container eBPF state so the next CP starts clean.
		// Errors are aggregated so a broken drain exits non-zero and the
		// on-failure restart policy legitimately retriggers investigation
		// rather than silently blessing partial teardown.
		handler.CancelAllBypassTimers()
		grpcServer.GracefulStop()
		var errs []error
		if err := stack.Stop(ctx); err != nil {
			log.Error().Err(err).Msg("drain: firewall stack stop failed")
			errs = append(errs, fmt.Errorf("stack stop: %w", err))
		}
		if err := ebpfMgr.FlushAll(); err != nil {
			log.Error().Err(err).Msg("drain: ebpf flush failed")
			errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
		}
		return errors.Join(errs...)
	}

	watcher := controlplane.NewAgentWatcher(log, listAgents, drainCallback, controlplane.AgentWatcherOptions{})
	watcherDone := make(chan error, 1)
	go func() {
		watcherDone <- watcher.Run(watcherCtx)
	}()

	log.Info().Msg("clawker-cp ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	var drainErr error
	select {
	case sig := <-sigCh:
		log.Info().Stringer("signal", sig).Msg("shutdown signal received")
		// Any subprocess exit past this point is part of graceful shutdown;
		// suppress crash reporting so it doesn't race with us.
		subMgr.BeginShutdown()
	case err := <-watcherDone:
		switch {
		case err == nil:
			log.Info().Msg("agent drain-to-zero — shutting down")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			log.Info().Err(err).Msg("agent watcher cancelled — shutting down")
		default:
			log.Error().Err(err).Msg("agent watcher error — shutting down")
			// Drain failures (stack stop / ebpf flush) must propagate so the
			// on-failure restart policy catches them.
			drainErr = err
		}
		subMgr.BeginShutdown()
	case err := <-subMgr.CrashChan():
		log.Error().Err(err).Msg("subprocess crashed — shutting down")
		return err
	case err := <-serveFailed:
		log.Error().Err(err).Msg("server failed — shutting down")
		return err
	}
	watcherCancel()

	// Reverse-order graceful shutdown.
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
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("healthz shutdown error")
	}

	subMgr.Shutdown(defaultShutdownWait)
	log.Info().Msg("clawker-cp stopped")
	return drainErr
}
