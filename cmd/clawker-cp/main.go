// clawker-cp is the containerized clawker control plane binary.
//
// It runs as the main process in the CP container, supervising Hydra,
// Oathkeeper, Kratos as subprocesses. It loads eBPF programs, serves a
// gRPC AdminService with Hydra token introspection, owns the Docker
// events feeder + informer, and reports readiness on /healthz.
//
// Oathkeeper runs as a subprocess for future webui HTTP auth. gRPC auth
// (CLI + agents) uses direct Hydra introspection — no Ory Go imports.
//
// The numbered startup sequence is documented in
// internal/controlplane/CLAUDE.md and not duplicated here so the two
// don't drift.
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
	"strings"
	"sync"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobyclient "github.com/moby/moby/client"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/controlplane/agent"
	"github.com/schmitthub/clawker/internal/controlplane/agentdial"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
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
	// cpDrainTimeout bounds the full teardown sequence (firewall stack
	// stop + eBPF flush + queue drain). Must be below the Docker SIGTERM
	// grace period (cpStopTimeout in cpboot/bootstrap.go = 30s) so we
	// finish before SIGKILL arrives. Envoy + CoreDNS each use Docker's
	// default 10s stop timeout, run sequentially → ~20s worst case,
	// leaving headroom.
	cpDrainTimeout = 25 * time.Second
)

func main() {
	caCertPath := flag.String("tls-ca", consts.CPCACertPath, "CLI CA certificate")
	serverCertPath := flag.String("tls-cert", consts.CPTLSCertPath, "TLS server certificate")
	serverKeyPath := flag.String("tls-key", consts.CPTLSKeyPath, "TLS server key")
	jwkPath := flag.String("jwk", consts.CPCLIPubKeyPath, "CLI signing JWK (bind-mounted)")
	logDir := flag.String("log-dir", "/var/log/clawker", "directory for persistent audit logs")
	flag.Parse()

	if err := run(*caCertPath, *serverCertPath, *serverKeyPath, *jwkPath, *logDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", consts.ContainerCP, err)
		os.Exit(1)
	}
}

func run(caCertPath, serverCertPath, serverKeyPath, jwkPath, logDir string) (retErr error) {
	loggerOpts := logger.Options{
		LogsDir:  logDir,
		Filename: consts.ControlPlaneLogFile,
		Otel:     otelOptionsFromEnv(),
	}
	log, err := logger.New(loggerOpts)
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
		"--config", consts.CPKratosConfigPath,
	)
	if err := subMgr.Start("kratos", kratosCmd); err != nil {
		return fmt.Errorf("step 1 (kratos): %w", err)
	}

	// --- Step 2: Start Hydra ---
	hydraCmd := exec.Command("hydra", "serve", "all",
		"--config", consts.CPHydraConfigPath,
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

	// --- Step 5: Register CLI + agent clients with Hydra ---
	// See controlplane.RegisterAgentClient for why both clients share
	// one JWK with distinct client_id + scope.
	hydraAdminURL := fmt.Sprintf("https://127.0.0.1:%d", cp.HydraAdminPort)
	if err := controlplane.RegisterCLIClient(ctx, hydraAdminURL, jwkData, caTLS); err != nil {
		return fmt.Errorf("step 5 (register CLI client): %w", err)
	}
	if err := controlplane.RegisterAgentClient(ctx, hydraAdminURL, jwkData, caTLS); err != nil {
		return fmt.Errorf("step 5 (register agent client): %w", err)
	}
	log.Info().Msg("CLI + agent clients registered with Hydra")

	// --- Step 6: Start Oathkeeper ---
	// Oathkeeper runs as an HTTP reverse proxy for future webui auth.
	// gRPC auth (CLI + agents) bypasses Oathkeeper entirely — it uses
	// direct Hydra token introspection via AuthInterceptor.
	oathkeeperCmd := exec.Command("oathkeeper", "serve",
		"--config", consts.CPOathkeeperConfigPath,
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
	// ebpfMgr.Close failures are joined with retErr so the on-failure
	// restart policy retriggers investigation rather than silently
	// blessing a partial teardown.
	defer func() {
		if err := ebpfMgr.Close(); err != nil {
			log.Error().Err(err).Msg("ebpf close error")
			retErr = errors.Join(retErr, fmt.Errorf("ebpf close: %w", err))
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

	// Auth interceptors: one per listener so each enforces its own
	// method-scope vocabulary. Both share a single Hydra introspector —
	// tokens are checked against the same Hydra instance regardless of
	// which listener received them.
	hydraIntrospectURL := fmt.Sprintf("https://127.0.0.1:%d/admin/oauth2/introspect", cp.HydraAdminPort)
	introspector := controlplane.NewHydraIntrospector(hydraIntrospectURL, caTLS)
	authInterceptor := controlplane.NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log)
	// Pin the agent interceptor to consts.ClientIDAgent — defense in
	// depth on top of the agent:self:register scope. Today only the
	// clawker-agent Hydra client is registered with that scope, so the
	// pin only fires if a future Hydra misconfiguration grants the
	// scope to another client. The admin interceptor stays unpinned —
	// the CLI is the only client that holds the admin scope and we
	// don't want to accidentally lock out a future second admin client.
	agentInterceptor := controlplane.
		NewAuthInterceptor(introspector, controlplane.AgentMethodScopes(), log).
		RequireClientID(consts.ClientIDAgent)

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor()),
	)

	// ActionQueue is the single-goroutine FIFO worker every Firewall*
	// RPC runs through so rule-mutation/stack-restart cycles never
	// collide. Constructed before the Handler so HandlerDeps.Queue is
	// non-nil at NewHandler time (NewHandler panics otherwise). The
	// final drain ordering (queue.Close → GracefulStop → bypass timer
	// cancel → stack.Stop → ebpf.FlushAll) is owned by the
	// drain-to-zero callback below so a single on-exit defer here is
	// sufficient as a belt-and-braces against non-drain exit paths.
	actionQueue := fwhandler.NewActionQueue(log)
	defer func() {
		if err := actionQueue.Close(); err != nil {
			log.Warn().Err(err).Msg("actionQueue close failed")
		}
	}()
	// listAgentIDs enumerates every running managed agent container ID.
	// Handler's FirewallInit uses this to re-enroll per-container BPF
	// enforcement after a CP restart — FlushAll wiped container_map on
	// the previous CP's shutdown, so a fresh FirewallInit rebuilds from
	// live Docker state instead of a silent fail-open.
	listAgentIDs := func(ctx context.Context) ([]string, error) {
		filter := mobyclient.Filters{}.
			Add("label", cfg.LabelManaged()+"="+cfg.ManagedLabelValue()).
			Add("label", cfg.LabelPurpose()+"="+cfg.PurposeAgent())
		result, err := dockerCli.APIClient.ContainerList(ctx, mobyclient.ContainerListOptions{Filters: filter})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(result.Items))
		for _, c := range result.Items {
			ids = append(ids, c.ID)
		}
		return ids, nil
	}
	handler := fwhandler.NewHandler(fwhandler.HandlerDeps{
		EBPF:       ebpfMgr,
		Stack:      stack,
		Store:      rulesStore,
		Cfg:        cfg,
		Resolver:   containerResolver,
		Log:        log,
		Queue:      actionQueue,
		ListAgents: listAgentIDs,
	})

	// Agent registry is needed BOTH by the AgentService handler (added
	// below on the agent listener) and by AdminService.ListAgents on the
	// admin listener — construct it here so a single instance is shared.
	// Backed by sqlite at consts.CPControlPlaneDBPath; the parent dir is
	// bind-mounted RW from the host, so the DB survives CP container
	// recreation and reloads on next boot.
	// CP opens the registry in read-only mode. The CLI is the sole
	// authoritative writer; CP consumes rows for dispatch decisions but
	// never mutates the row set. mode=ro+query_only=ON is the
	// belt-and-suspenders guarantee — the bind mount is RW at the OS
	// layer so sqlite can read the WAL/SHM siblings, but writes are
	// blocked at the connection.
	agentReg, err := agentregistry.NewSQLiteReader(consts.CPControlPlaneDBPath, log.With("component", "agentregistry"))
	if err != nil {
		return fmt.Errorf("step 8 (agentregistry sqlite): %w", err)
	}

	// announceSlots holds the short-lived CLI-attestation tokens that
	// AdminService.AnnounceAgent reserves (per-start, container_id
	// keyed). Consumed by agentdial when CP dials the running
	// clawkerd; absence is the "unattested start" signal for
	// downstream decision-making, not an error.
	announceSlotRegistry := agentslots.NewRegistry(time.Now, 0, log.With("component", "announceslots"))
	defer announceSlotRegistry.Stop()

	// agentslots is the legacy PKCE+thumbprint slot store backing the
	// dead AgentService.Register handler. Kept wired so the handler
	// surface compiles and stays in place for future agent→CP RPCs;
	// nothing in this branch calls Register.
	slotRegistry := agentslots.NewRegistry(time.Now, 0, log.With("component", "agentslots"))
	defer slotRegistry.Stop()

	adminv1.RegisterAdminServiceServer(grpcServer, controlplane.NewAdminServer(handler, agentReg, announceSlotRegistry, time.Now, log))

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(cp.AdminPort))
	if err != nil {
		return fmt.Errorf("step 8 (grpc listen): %w", err)
	}

	// Agent listener — bound to clawker-net only (NOT host-published).
	// Same mTLS material as the admin listener (server cert + CLI CA
	// pool); the per-listener AuthInterceptor enforces the agent-side
	// method-scope vocabulary so admin and agent surfaces fail closed
	// on cross-listener method names.
	agentTLSCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13,
	}
	// IdentityInterceptor runs AFTER AuthInterceptor: token + scope
	// pass first, then identity resolves the peer cert thumbprint to
	// a registered agent (or rejects). Connect is on the opt-out list
	// (it authenticates itself); every other agent RPC must be
	// registry-bound by the time the handler sees it.
	identityUnary, identityStream := agent.IdentityInterceptor(
		agentReg,
		agent.IdentityOptedOutMethods(),
		log.With("component", "agent-identity"),
	)
	agentServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(agentTLSCfg)),
		grpc.ChainUnaryInterceptor(agentInterceptor.UnaryInterceptor(), identityUnary),
		grpc.ChainStreamInterceptor(agentInterceptor.StreamInterceptor(), identityStream),
	)
	agentLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(cp.AgentPort))
	if err != nil {
		return fmt.Errorf("step 8 (agent grpc listen): %w", err)
	}

	// AgentService wiring: shared slot registry (announce-time
	// reservations, hoisted above NewAdminServer) + the shared agent
	// registry + handler. The agent registry's dockerevents
	// subscription is set up further down, once the informer is alive.
	agentInspector := agent.MobyInspector{Client: dockerCli.APIClient}
	agentHandler := agent.NewHandler(slotRegistry, agentReg, agentInspector, log.With("component", "agent-handler"))
	agentv1.RegisterAgentServiceServer(agentServer, agentHandler)

	// Cap covers gRPC admin, gRPC agent, healthz, and the dockerevents
	// feeder. Buffered so any goroutine that fails before main reaches
	// the select can deposit its error without blocking.
	serveFailed := make(chan error, 4)

	go func() {
		log.Info().Int("port", cp.AdminPort).Msg("gRPC admin API serving")
		if err := grpcServer.Serve(grpcLis); err != nil {
			serveFailed <- fmt.Errorf("gRPC admin serve: %w", err)
		}
	}()

	go func() {
		log.Info().Int("port", cp.AgentPort).Msg("gRPC agent API serving")
		if err := agentServer.Serve(agentLis); err != nil {
			serveFailed <- fmt.Errorf("gRPC agent serve: %w", err)
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
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveFailed <- fmt.Errorf("healthz serve: %w", err)
		}
	}()

	// Step 9a: Informer + dockerevents feeder.
	//
	// The informer is the in-process realm model. It outlives every
	// individual feeder/consumer for the daemon's lifetime; we close it
	// explicitly during the drain sequence below (after ebpf flush) so
	// any final dispatched events have a fully-functional informer to
	// land on. The dockerevents feeder pushes a clawker-managed mirror
	// of Docker state into it.
	// WriteQueueSize=2048: high enough to absorb a docker events burst
	// (image prune + network reconnect storm) without blocking the feeder
	// goroutine but bounded so a stuck consumer doesn't grow unbounded.
	// SubscriberBuffer=256: per-subscriber drop-oldest threshold; sized
	// so a slow consumer only loses ~5s of activity at the heartbeat
	// rate before deltas start dropping.
	inf := informer.New(informer.Options{
		Logger:           log.With("component", "informer"),
		WriteQueueSize:   2048,
		SubscriberBuffer: 256,
	})

	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()

	if err := inf.Start(watcherCtx); err != nil {
		return fmt.Errorf("step 9a (informer start): %w", err)
	}

	feeder, err := dockerevents.New(dockerCli.APIClient, inf, dockerevents.Options{
		ManagedLabelKey:   cfg.LabelManaged(),
		ManagedLabelValue: cfg.ManagedLabelValue(),
		Logger:            log,
	})
	if err != nil {
		return fmt.Errorf("step 9a (dockerevents feeder): %w", err)
	}
	// feederCtx is a child of watcherCtx so SIGTERM/drain-to-zero both
	// reach it; feederCancel exists separately so drainCallbackBody can
	// stop the feeder BEFORE closing the informer (avoids ErrClosed
	// noise on in-flight Upsert calls when AgentWatcher fires the drain
	// while watcherCtx is still alive).
	feederCtx, feederCancel := context.WithCancel(watcherCtx)
	defer feederCancel()

	// Hook agent registry to dockerevents container deltas — evicts
	// registered agents when their containers die/destroy. Subscribe
	// runs through the informer so the same drain that closes the
	// informer also tears this down cleanly.
	cancelAgentSub := agentregistry.Subscribe(watcherCtx, agentReg, inf, log.With("component", "agentregistry"))
	defer cancelAgentSub()

	// Same wiring for the slot registry: a pending slot whose
	// container died (e.g. ContainerStart failed mid-bootstrap) is
	// dead-on-arrival; the TTL janitor would eventually sweep, but
	// the dockerevents-driven path evicts immediately so a quick retry
	// can re-announce without an ErrSlotExists collision.
	cancelSlotSub := agentslots.Subscribe(watcherCtx, slotRegistry, inf, log.With("component", "agentslots"))
	defer cancelSlotSub()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		err := feeder.Run(feederCtx)
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		// Non-cancel exit: the feeder's Run loop is supposed to retry
		// internally on every reconcile/stream error. A real return
		// means a wiring bug or unrecoverable contract violation —
		// surface to serveFailed so the daemon exits non-zero and the
		// on-failure restart policy retriggers.
		log.Error().Err(err).Msg("dockerevents feeder exited with error")
		select {
		case serveFailed <- fmt.Errorf("dockerevents feeder: %w", err):
		default:
		}
	}()

	// Periodic informer stats heartbeat — gives an operator tailing the
	// CP log (or querying Loki) a coarse health signal without needing
	// a dedicated metrics surface. 30s cadence is below the OTEL
	// resilience window and trivial overhead.
	statsCtx, statsCancel := context.WithCancel(watcherCtx)
	defer statsCancel()
	go func() {
		// recover so a future Stats() panic doesn't silently kill the
		// heartbeat loop and leave the operator without telemetry.
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("informer stats heartbeat panicked")
			}
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-statsCtx.Done():
				return
			case <-ticker.C:
				st := inf.Stats()
				log.Info().
					Int("resources", st.Resources).
					Int("relations", st.Relations).
					Int("subscribers", st.Subscribers).
					Uint64("writes_total", st.WritesTotal).
					Uint64("deltas_emitted_total", st.DeltasEmittedTotal).
					Uint64("deltas_dropped_total", st.DeltasDroppedTotal).
					Int("queue_depth", st.QueueDepth).
					Int("queue_capacity", st.QueueCapacity).
					Msg("informer stats heartbeat")
			}
		}
	}()

	listAgents := func(ctx context.Context) (int, error) {
		ids, err := listAgentIDs(ctx)
		if err != nil {
			return 0, err
		}
		return len(ids), nil
	}

	drainCallbackBody := func(ctx context.Context) error {
		// Strict ordering (INV-B2-007):
		//  1. actionQueue.Close drains accepted submissions to completion
		//     then returns ErrClosed for any subsequent Submit — the
		//     Handler's bypass-timer goroutines observe this and exit
		//     cleanly instead of racing with FlushAll.
		//  2. grpcServer.GracefulStop refuses new RPCs and waits for
		//     in-flight handlers to return. With the queue closed any
		//     handler still running hits ErrClosed from its pending
		//     Submit and returns, so GracefulStop unblocks quickly.
		//  3. Cancel any bypass timer that was mid-retry when Close
		//     landed; safe no-op if the queue already drained them.
		//  4. Stop the firewall stack (Envoy + CoreDNS).
		//  5. Flush per-container eBPF state so the next CP starts clean.
		// Errors are aggregated so a broken drain exits non-zero and the
		// on-failure restart policy retriggers investigation rather than
		// silently blessing partial teardown.
		if err := actionQueue.Close(); err != nil {
			log.Warn().Err(err).Msg("actionQueue close failed")
		}
		grpcServer.GracefulStop()
		handler.CancelAllBypassTimers()
		var errs []error
		if err := stack.Stop(ctx); err != nil {
			log.Error().Err(err).Msg("drain: firewall stack stop failed")
			errs = append(errs, fmt.Errorf("stack stop: %w", err))
		}
		if err := ebpfMgr.FlushAll(); err != nil {
			log.Error().Err(err).Msg("drain: ebpf flush failed")
			errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
		}
		// Stop the feeder before closing the informer so any in-flight
		// Upsert lands cleanly. feederCancel is idempotent; feederDone
		// closes once the goroutine returns.
		feederCancel()
		<-feederDone
		if err := inf.Close(); err != nil {
			log.Error().Err(err).Msg("drain: informer close failed")
			errs = append(errs, fmt.Errorf("informer close: %w", err))
		}
		return errors.Join(errs...)
	}

	// drainCallback wraps the body in sync.Once so SIGTERM and
	// drain-to-zero converge on the same teardown. Whichever trigger
	// wins runs the full sequence exactly once; the other observes the
	// captured error via drainErr. This is what lets `clawker
	// controlplane down` leave no orphan Envoy/CoreDNS containers —
	// `docker stop` sends SIGTERM to PID 1, which now runs the same
	// teardown as drain-to-zero.
	var (
		drainOnce sync.Once
		drainErr  error
	)
	drainCallback := func(ctx context.Context) error {
		drainOnce.Do(func() { drainErr = drainCallbackBody(ctx) })
		return drainErr
	}

	watcher := controlplane.NewAgentWatcher(log, listAgents, drainCallback, controlplane.AgentWatcherOptions{})
	watcherDone := make(chan error, 1)
	go func() {
		watcherDone <- watcher.Run(watcherCtx)
	}()

	// CP→clawkerd dial reconciler. Initial poll: open a Session to
	// every running purpose=agent container so command dispatch is
	// ready by the time anything wants to dispatch. The same DialAgent
	// is the call target for the dockerevents container-start path
	// added next; one dial code path, two callers.
	//
	// CP-readiness must NOT block on this. Failures (cert load, list
	// containers, individual dial) are logged and the rest of CP
	// proceeds — a misconfigured agent or a flapping clawkerd cannot
	// hold the control plane down.
	dialer, err := agentdial.New(
		log.With("component", "agentdial"),
		dockerCli.APIClient,
		inf,
		agentReg,
		announceSlotRegistry,
		consts.CPClientCertPath,
		consts.CPClientKeyPath,
		caCertPool,
	)
	if err != nil {
		log.Error().Err(err).Str("event", "agentdial_init_failed").Msg("agentdial unavailable; CP→clawkerd dispatch disabled")
		dialer = nil
	}
	if dialer != nil {
		// Runtime path: dockerevents → informer → agentdial.Subscribe
		// reacts to "container started" deltas for purpose=agent
		// containers and dials. Same DialAgent function the initial
		// poll calls; dedup map on Dialer prevents the two paths
		// from double-dialing the same containerID.
		cancelDialSub := agentdial.Subscribe(watcherCtx, dialer, inf, log.With("component", "agentdial"))
		defer cancelDialSub()

		// Initial path: dial every already-running agent container at
		// CP boot. Runs in its own goroutine — must NOT block CP
		// readiness, must NOT fail CP if listAgentIDs errors.
		go func() {
			initialAgents, listErr := listAgentIDs(watcherCtx)
			if listErr != nil {
				log.Error().Err(listErr).Str("event", "agentdial_initial_list_failed").Msg("list agent containers")
				return
			}
			for _, id := range initialAgents {
				dialer.DialAgent(watcherCtx, id)
			}
			log.Info().Int("count", len(initialAgents)).Str("event", "agentdial_initial_poll_dispatched").Msg("dispatched initial CP→clawkerd dials")
		}()
	}

	log.Info().Msg("clawker-cp ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		log.Info().Stringer("signal", sig).Msg("shutdown signal received")
		// Any subprocess exit past this point is part of graceful shutdown;
		// suppress crash reporting so it doesn't race with us.
		subMgr.BeginShutdown()
		// Cancel the watcher and wait for it to exit so there's no race
		// on drainCallback — sync.Once makes the actual teardown safe
		// either way, but we want a deterministic ordering for logs.
		watcherCancel()
		<-watcherDone
		// Run the full teardown: Envoy + CoreDNS stop, eBPF flush,
		// queue drain. Without this, a `docker stop clawker-controlplane`
		// (i.e. `clawker controlplane down`) would leave orphan firewall
		// containers and stale BPF map state.
		teardownCtx, teardownCancel := context.WithTimeout(context.Background(), cpDrainTimeout)
		if err := drainCallback(teardownCtx); err != nil {
			log.Error().Err(err).Msg("sigterm teardown failed")
		}
		teardownCancel()
	case err := <-watcherDone:
		switch {
		case err == nil:
			log.Info().Msg("agent drain-to-zero — shutting down")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			log.Info().Err(err).Msg("agent watcher cancelled — shutting down")
		default:
			log.Error().Err(err).Msg("agent watcher error — shutting down")
			// Drain failures (stack stop / ebpf flush) already captured
			// in drainErr via the sync.Once wrapper; the watcher just
			// surfaced them.
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
	wg.Add(2)
	go func() {
		defer wg.Done()
		grpcServer.GracefulStop()
	}()
	go func() {
		defer wg.Done()
		agentServer.GracefulStop()
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
		agentServer.Stop()
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

// otelOptionsFromEnv builds logger.OtelOptions from the standard OTLP
// environment variables. Returns nil when no endpoint is configured —
// the logger then runs file-only and the CP daemon needs no OTEL
// dependency at runtime.
//
// Per-signal `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` takes precedence over
// the generic `OTEL_EXPORTER_OTLP_ENDPOINT`. Either may be a full URL
// (`https://host.docker.internal:4319/v1/logs`) or a bare authority
// (`host.docker.internal:4319`); the otlploghttp exporter only needs
// host:port, so we strip scheme/path here.
//
// Default is TLS. Bare host:port → TLS. `https://` → TLS. Only
// explicit `http://` opts in to plaintext, so a misconfigured prod
// endpoint can't silently downgrade.
//
// mTLS material is read from `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE`,
// `OTEL_EXPORTER_OTLP_CLIENT_KEY`, and `OTEL_EXPORTER_OTLP_CERTIFICATE`
// (the trust bundle for the receiver). When all three are present
// the exporter does mTLS.
//
// The bridge endpoint is set up by cpboot to point at the monitor
// stack's CP-only receiver via host.docker.internal. CP is BPF-exempt
// (not enrolled in container_map) and ExtraHosts maps the gateway
// alias, so the dial reaches the host loopback published port. Agents
// on clawker-net cannot reach this endpoint AND cannot present a
// CLI-signed client cert — two layers of isolation.
func otelOptionsFromEnv() *logger.OtelOptions {
	raw := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	if raw == "" {
		raw = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if raw == "" {
		return nil
	}

	endpoint, insecure := parseOtlpEndpoint(raw)
	if endpoint == "" {
		return nil
	}
	opts := &logger.OtelOptions{
		Endpoint: endpoint,
		Insecure: insecure,
	}
	if cert := os.Getenv("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"); cert != "" {
		opts.ClientCertFile = cert
		opts.ClientKeyFile = os.Getenv("OTEL_EXPORTER_OTLP_CLIENT_KEY")
		opts.CACertFile = os.Getenv("OTEL_EXPORTER_OTLP_CERTIFICATE")
		// mTLS implies TLS; insecure must be false even if the env URL
		// was http:// (which would be a misconfiguration but we
		// override defensively).
		opts.Insecure = false
	}
	return opts
}

// parseOtlpEndpoint normalises an OTEL endpoint env value to the
// host:port form `otlploghttp.WithEndpoint` accepts, returning whether
// it should be sent plaintext.
//
// Default is secure. Only an explicit `http://` scheme opts into
// plaintext — a bare host:port or `https://` use TLS so a
// misconfigured prod env can't silently downgrade to cleartext logs.
func parseOtlpEndpoint(raw string) (endpoint string, insecure bool) {
	rest := raw
	switch {
	case strings.HasPrefix(rest, "https://"):
		rest = strings.TrimPrefix(rest, "https://")
	case strings.HasPrefix(rest, "http://"):
		insecure = true
		rest = strings.TrimPrefix(rest, "http://")
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest, insecure
}
