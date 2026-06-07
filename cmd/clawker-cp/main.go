// clawker-cp is the containerized clawker control plane binary.
//
// It runs as the main process in the CP container, supervising Hydra,
// Oathkeeper, Kratos as subprocesses. It loads eBPF programs, serves a
// gRPC AdminService with Hydra token introspection, owns the Docker
// events feeder + Overseer bus, and reports readiness on /healthz.
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
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	fwhandler "github.com/schmitthub/clawker/internal/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf/netlogger"
	"github.com/schmitthub/clawker/internal/controlplane/infracerts"
	"github.com/schmitthub/clawker/internal/controlplane/otelcerts"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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
	// dnsGCInterval is how often the CP sweeps expired entries out of the
	// pinned dns_cache. CoreDNS (dnsbpf) writes one entry per resolved A
	// record and nothing else reclaims them, so without this sweep the map
	// grows unbounded over the CP lifetime and entries for since-removed
	// zones linger as orphaned hashes (surfacing via netlogger as
	// event=netlogger_reverse_dns_unattributed). IP-literal seeds are
	// protected by GarbageCollectDNS (m.seededIPs), so a live IP rule's
	// route is never reclaimed out from under it.
	dnsGCInterval = 60 * time.Second
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
	// Load the infra intermediate + build the trusted-lane OTel cert
	// provisioner BEFORE logger.New. The logger's OTLP exporter is
	// locked in at construction time — it has no hot-reconfig hook —
	// so the TLSConfig must be available at that moment. infracerts is
	// cheap (reads two on-disk PEM files), so loading it early is
	// strictly safer than late-binding a logger exporter that would
	// otherwise need env-driven cert paths — agent containers carry
	// CLI-root-direct leaves, so any env-readable cert path becomes a
	// smuggling vector for service.name=clawker-cp forgery.
	//
	// Defer all error logging until after logger.New so failures land
	// in the structured log surface that operators are wired to watch,
	// not stderr.
	// Two-variable pattern is load-bearing for safe degraded-mode
	// signaling: otelCerts (interface type) must remain plain
	// interface-nil on any failure so the firewall stack's
	// `s.otelCerts == nil` check fires. Assigning a typed-nil
	// `(*otelcerts.Service)(nil)` into the interface would box
	// non-nil, defeat the nil check, and dispatch EnsureClient on a
	// nil receiver — turning the intended degraded mode into a panic
	// that strands eBPF (see internal/controlplane/CLAUDE.md, "CP
	// crashing is a security incident"). Keep the concrete *Service
	// in `otelCertsSvc` for the post-logger SetLogger wiring; only
	// assign into `otelCerts` on the success path.
	var (
		otelCertsSvc     *otelcerts.Service
		otelCerts        fwhandler.OtelCertProvisioner
		otelTLSConfig    *tls.Config
		otelCertsErr     error
		otelCertsErrStep string
	)
	if issuer, err := infracerts.Load(consts.CPInfraCACertPath, consts.CPInfraCAKeyPath); err != nil {
		otelCertsErr = fmt.Errorf("infracerts load: %w", err)
		otelCertsErrStep = "infracerts_load"
	} else if otelDir, err := consts.OtelClientsDir(); err != nil {
		otelCertsErr = fmt.Errorf("resolving otel-clients dir: %w", err)
		otelCertsErrStep = "otel_clients_dir"
	} else if rootCABytes, err := os.ReadFile(consts.CPCACertPath); err != nil {
		otelCertsErr = fmt.Errorf("reading CLI root CA at %s: %w", consts.CPCACertPath, err)
		otelCertsErrStep = "read_root_ca"
	} else if svc, err := otelcerts.New(issuer, otelDir, rootCABytes, nil); err != nil {
		otelCertsErr = fmt.Errorf("constructing otelcerts.Service: %w", err)
		otelCertsErrStep = "service_new"
	} else if tlsCfg, err := svc.LoadTLSConfig("cp"); err != nil {
		otelCertsErr = fmt.Errorf("building cp tls.Config: %w", err)
		otelCertsErrStep = "load_tls_config"
	} else {
		otelCertsSvc = svc
		otelCerts = svc
		otelTLSConfig = tlsCfg
	}

	// Wire the in-process TLSConfig into the logger's OTLP exporter.
	// On any failure above, drop OtelOptions entirely (not just
	// TLSConfig) so the logger stays file+stderr only — never half-
	// init mTLS with no creds (would spam handshake failures) and
	// never plaintext-fall-back to the untrusted lane.
	otelOpts := otelOptionsFromEnv()
	if otelTLSConfig != nil && otelOpts != nil {
		otelOpts.TLSConfig = otelTLSConfig
	} else {
		otelOpts = nil
	}
	loggerOpts := logger.Options{
		LogsDir:  logDir,
		Filename: consts.ControlPlaneLogFile,
		Otel:     otelOpts,
		// Mirror structured records to stdout so `docker logs
		// clawker-controlplane` shows what the file/OTEL sinks see.
		EchoStdout: true,
	}
	log, err := logger.New(loggerOpts)
	loggerInitErr := err
	if err != nil {
		// Fall back to stderr-only if log dir isn't writable.
		log = logger.NewWriter(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s: warning: file logging unavailable (%v), using stderr only\n", consts.ContainerCP, err)
	}
	log = log.With("component", consts.ContainerCP)
	defer log.Close()
	log.Info().Msg("starting")

	// Wire the real logger into the Service now that logger.New has
	// succeeded. LoadTLSConfig's closure reads s.log lazily (at
	// handshake time, well after this point), so post-construction
	// SetLogger is safe and lets the per-handshake mint-failure event
	// surface in the structured log.
	if otelCertsSvc != nil {
		otelCertsSvc.SetLogger(log)
	}

	if loggerInitErr != nil {
		// Surface the file-logger fallback as a structured event so
		// the docker-logs surface (the only place stderr lands) at
		// least contains a discoverable record. Note: the OTel bridge
		// is also gone in this path because logger.NewWriter takes no
		// OtelOptions — operators must know the trusted lane is dark.
		log.Error().Err(loggerInitErr).
			Str("event", "file_logger_unavailable").
			Str("component", "logger").
			Msg("file logging init failed — running with stderr-only logger, OTel bridge disabled")
	}

	if otelCertsErr != nil {
		log.Error().Err(otelCertsErr).
			Str("event", "otelcerts_unavailable").
			Str("component", "otelcerts").
			Str("step", otelCertsErrStep).
			Msg("trusted-lane OTLP push disabled — CP and infra services run with file/stderr logs only")
	}

	logHostIdentity(log, consts.HostUIDResolution(), consts.HostGIDResolution())

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
	// Infra intermediate CA — bind-mounted RO by cpboot at startup.
	// firewall.Stack uses it to mint short-lived mTLS client leaves
	// otelCerts was wired pre-logger; both the firewall.Stack (for
	// envoy/coredns leaf provisioning) and the CP's own OTLP exporter
	// consume it. Degraded mode (nil otelCerts) is already logged via
	// the event=infra_issuer_unavailable line above; container specs
	// drop the mTLS bind-mounts and CP's OTel push lane is closed.
	stack := fwhandler.NewStack(dockerCli, cfg, log, rulesStore, otelCerts)

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

	// Overseer bus: constructed + Started here, BEFORE the gRPC
	// listener accepts. FirewallEnable publishes
	// ebpf.EBPFContainerEnrolled on the bus, so the bus MUST be
	// Started before the first inbound RPC can fire — otherwise the
	// enroll event drops silently and downstream subscribers miss
	// the binding for that cgroup. The dockerevents feeder +
	// agent.Start subscribers still wire up in step 9a below.
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()

	busLog := log.With("component", "overseer")
	bus := overseer.New(overseer.Options{
		Logger:            busLog,
		PublishBufferSize: 2048,
		SubscriberBuffer:  256,
		// PublishHook emits one structured Info line per published
		// event from the bus loop. Producers (dockerevents, agent,
		// firewall) no longer pair manual log calls with each Publish
		// — the hook is the single canonical source of bus-event log
		// lines.
		PublishHook: overseer.NewLoggerHook(busLog),
	})
	if err := bus.Start(watcherCtx); err != nil {
		return fmt.Errorf("step 8 (overseer start): %w", err)
	}
	// listAgentIDs enumerates managed agent container IDs. Two callers
	// at two scopes:
	//   - Firewall handler + AgentWatcher pass {} → running only. They
	//     drive per-container BPF enforcement and drain-to-zero, both
	//     of which only care about live containers.
	//   - Reaper passes {All: true} → running + stopped + exited. A
	//     stopped container can be `docker start`-ed back into life so
	//     its registry row must survive; only `docker rm` orphans it.
	// The label filter is non-overridable so a caller can't accidentally
	// widen scope past purpose=agent.
	type listAgentsOpts struct{ All bool }
	listAgentIDs := func(ctx context.Context, opts listAgentsOpts) ([]string, error) {
		filter := mobyclient.Filters{}.
			Add("label", cfg.LabelManaged()+"="+cfg.ManagedLabelValue()).
			Add("label", cfg.LabelPurpose()+"="+cfg.PurposeAgent())
		result, err := dockerCli.APIClient.ContainerList(ctx, mobyclient.ContainerListOptions{
			All:     opts.All,
			Filters: filter,
		})
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
		Bus:        bus,
		ListAgents: func(ctx context.Context) ([]string, error) { return listAgentIDs(ctx, listAgentsOpts{}) },
	})

	// Agent registry is needed BOTH by the Register handler on the
	// agent listener and by AdminService.ListAgents on the admin
	// listener — construct it here so a single instance is shared.
	// Backed by sqlite at consts.CPControlPlaneDBPath; the parent dir
	// is bind-mounted RW from the host, so the DB survives CP container
	// recreation and reloads on next boot.
	//
	// CP is the SOLE sqlite writer: it captures peer cert thumbprints
	// at Register handler entry and writes the row, evicts on
	// container/destroy via dockerevents, and reaps orphan rows at
	// startup. The host CLI never opens this DB — that's what fixes
	// the WAL coherence bug across the macOS bind-mount boundary.
	//
	// EnsureSchema runs first so a fresh CP container against an empty
	// data dir comes up with the schema applied before NewSQLiteWriter
	// queries SELECT COUNT (which would otherwise see "no such table").
	if err := agent.EnsureSchema(consts.CPControlPlaneDBPath, log.With("component", "agent")); err != nil {
		return fmt.Errorf("step 8 (agent ensure schema): %w", err)
	}
	agentReg, err := agent.NewSQLiteWriter(consts.CPControlPlaneDBPath, log.With("component", "agent"))
	if err != nil {
		return fmt.Errorf("step 8 (agent sqlite): %w", err)
	}

	// Peer-IP→Docker→labels resolver. Maps a live mTLS peer IP to
	// the `purpose=agent` container owning that endpoint on
	// clawker-net so the identity surface can ground its trust check
	// on a kernel-attested source instead of cert claims.
	agentPeerLookup := agent.NewMobyPeerLookup(dockerCli.APIClient, log.With("component", "agent-peer-lookup"))

	adminv1.RegisterAdminServiceServer(grpcServer, controlplane.NewAdminServer(handler, agentReg, log))

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
	// pass first, then the universal identity gate grounds trust in
	// the kernel-attested peer IP (peer-IP → Docker → labels) and
	// verifies the cert's urn:clawker:agent: URI SAN against the
	// label-derived AgentFullName. Applies to every RPC including
	// Register — no opt-out exists. A constructor failure (nil
	// resolver — wiring regression) degrades the AgentService surface:
	// no agent listener brought up, no Register handler registered;
	// CP, firewall, registry, AdminService stay up so operators can
	// still observe and contain. Failing closed here is correct — a
	// half-wired trust gate is exactly the silent-failure surface root
	// CLAUDE.md forbids.
	identityUnary, identityStream, identityErr := agent.IdentityInterceptor(
		agentPeerLookup,
		log.With("component", "agent-identity"),
	)
	if identityErr != nil {
		log.Error().Err(identityErr).
			Str("component", "agent-identity").
			Str("event", "agent_identity_unavailable").
			Msg("agent identity gate unavailable; AgentService listener disabled, CP serve path otherwise unaffected")
	}

	var agentServer *grpc.Server
	var agentLis net.Listener
	if identityUnary != nil {
		agentServer = grpc.NewServer(
			grpc.Creds(credentials.NewTLS(agentTLSCfg)),
			grpc.ChainUnaryInterceptor(agentInterceptor.UnaryInterceptor(), identityUnary),
			grpc.ChainStreamInterceptor(agentInterceptor.StreamInterceptor(), identityStream),
		)
		agentLis, err = net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(cp.AgentPort))
		if err != nil {
			return fmt.Errorf("step 8 (agent grpc listen): %w", err)
		}

		// Register the AgentService.Register handler. IdentityInterceptor
		// has already grounded the peer in a daemon-resolved container
		// identity and attached it to ctx; the handler captures the cert
		// thumbprint, cross-checks the cert's container_id SAN + request
		// fields against the resolved truth, and writes the agentregistry
		// row. The handler is the SOLE writer of the agentregistry sqlite
		// DB.
		registerHandler, herr := agent.NewHandler(
			agentReg,
			log.With("component", "agent-register"),
		)
		if herr != nil {
			return fmt.Errorf("step 8 (agent register handler): %w", herr)
		}
		agentv1.RegisterAgentServiceServer(agentServer, registerHandler)
	}

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

	if agentServer != nil {
		go func() {
			log.Info().Int("port", cp.AgentPort).Msg("gRPC agent API serving")
			if err := agentServer.Serve(agentLis); err != nil {
				serveFailed <- fmt.Errorf("gRPC agent serve: %w", err)
			}
		}()
	}

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

	// Step 9a: dockerevents feeder.
	//
	// Wires the dockerevents feeder onto the bus created in step 8;
	// agent.Start (below) hangs its container/{start,destroy}
	// subscribers off the same bus.
	feeder, err := dockerevents.New(dockerCli.APIClient, bus, dockerevents.Options{
		ManagedLabelKey:   cfg.LabelManaged(),
		ManagedLabelValue: cfg.ManagedLabelValue(),
		Logger:            log,
	})
	if err != nil {
		return fmt.Errorf("step 9a (dockerevents feeder): %w", err)
	}
	// feederCtx is a child of watcherCtx so SIGTERM/drain-to-zero both
	// reach it; feederCancel exists separately so drainCallbackBody can
	// stop the feeder BEFORE closing the bus (avoids dropped-publish
	// noise on in-flight events when AgentWatcher fires the drain
	// while watcherCtx is still alive).
	feederCtx, feederCancel := context.WithCancel(watcherCtx)
	defer feederCancel()

	// agentCleanup is wired below once the dialer is constructed —
	// agent.Start is the single entry point that consolidates startup
	// reap, container/destroy → registry evict, and container/start →
	// dial agent into one bundle. Initialized to a no-op so deferred
	// cleanup is safe even if Start fails before assignment.
	agentCleanup := func() {}
	defer func() { agentCleanup() }()

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

	// Periodic Overseer stats heartbeat — gives an operator tailing
	// the CP log (or querying OpenSearch) a coarse health signal
	// without needing a dedicated metrics surface. 30s cadence is
	// below the OTEL resilience window and trivial overhead.
	statsCtx, statsCancel := context.WithCancel(watcherCtx)
	defer statsCancel()
	go func() {
		// recover so a future Stats() panic doesn't silently kill the
		// heartbeat loop and leave the operator without telemetry.
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("overseer stats heartbeat panicked")
			}
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-statsCtx.Done():
				return
			case <-ticker.C:
				st := bus.Stats()
				log.Info().
					Int("subscribers", st.Subscribers).
					Uint64("published_total", st.PublishedTotal).
					Uint64("dropped_total", st.DroppedTotal).
					Int("queue_depth", st.QueueDepth).
					Int("queue_capacity", st.QueueCapacity).
					Int("containers_known", st.ContainersKnown).
					Int("sessions_known", st.SessionsKnown).
					Msg("overseer stats heartbeat")
			}
		}
	}()

	// Step 9c: netlogger — eBPF egress event emitter. Drains the
	// per-decision-point ringbuf populated by BPF and pushes enriched
	// records to the trusted-infra OTLP receiver over a distinct
	// service.name. Degraded paths (no otelcerts, no infra endpoint
	// in env, provider preflight failure, netlogger constructor
	// error, Start failure) each emit event=netlogger_unavailable
	// and leave netloggerSvc nil so the drain hook skips the Stop
	// call. CP, firewall, and gRPC surface stay up.
	//
	// netloggerProvider is hoisted to the run scope so the drain
	// callback can Shutdown it after netloggerSvc.Stop — netlogger
	// deliberately does not own the provider's lifetime (see
	// netlogger/CLAUDE.md), so a clean drain has to flush it from
	// here or the BatchProcessor goroutine leaks past Stop without
	// flushing in-flight batches.
	var (
		netloggerSvc      *netlogger.Service
		netloggerProvider *sdklog.LoggerProvider
	)
	{
		endpoint, insecure := "", false
		if raw := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); raw != "" {
			endpoint, insecure = parseOtlpEndpoint(raw)
		} else if raw := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); raw != "" {
			endpoint, insecure = parseOtlpEndpoint(raw)
		}

		// unconfigured tracks the "operator never set an OTLP
		// endpoint" path so the structured log line lands at Warn
		// instead of Error. That case is a normal optional-monitoring
		// deployment shape; an operator log surface that screams
		// Error on every default-config boot trains them to filter
		// netlogger_unavailable, masking real failures later.
		unconfigured := false
		var degradeErr error
		var reason string
		switch {
		case otelCertsSvc == nil:
			reason = "otelcerts unavailable"
			degradeErr = fmt.Errorf("trusted-lane TLS material absent")
		case endpoint == "":
			reason = "no OTLP endpoint configured"
			degradeErr = fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT not set")
			unconfigured = true
		case insecure:
			// Trust lane requires mTLS — never push BPF telemetry
			// over plaintext. An operator who configured http://
			// for the CP zerolog bridge is signalling test-mode,
			// not production-mode; degrade rather than smuggle
			// records onto a non-trusted receiver.
			reason = "OTLP endpoint is plaintext"
			degradeErr = fmt.Errorf("netlogger requires mTLS endpoint, got insecure: %s", endpoint)
		}

		if degradeErr == nil {
			tlsCfg, err := otelCertsSvc.LoadTLSConfig("netlogger")
			if err != nil {
				reason = "LoadTLSConfig"
				degradeErr = fmt.Errorf("netlogger LoadTLSConfig: %w", err)
			} else {
				// Circuit breaker: 3 consecutive Export failures
				// trip the breaker permanently for the rest of the
				// CP lifetime. The counting-exporter wrap that the
				// initiative listed for Prom metrics is deferred to
				// a follow-up PR (see netlogger/metrics.go) — only
				// the circuit breaker is wired today.
				circLog := log.With("component", "netlogger.circuit")
				wrap := func(inner sdklog.Exporter) sdklog.Exporter {
					return netlogger.NewCircuitExporter(inner, netlogger.CircuitOptions{
						FailureThreshold: 3,
						Log:              circLog,
					})
				}
				netloggerProvider, err = controlplane.NewOtelLoggerProvider(controlplane.OtelClientOptions{
					Endpoint:            endpoint,
					TLSConfig:           tlsCfg,
					ServiceName:         "ebpf-egress",
					MaxQueueSize:        2048,
					ExportInterval:      time.Second,
					ExportTimeout:       30 * time.Second,
					RetryMaxElapsedTime: 10 * time.Second,
					PreflightTimeout:    20 * time.Second,
					Log:                 log,
					ExporterWrap:        wrap,
				})
				if err != nil {
					reason = "NewOtelLoggerProvider"
					degradeErr = fmt.Errorf("netlogger NewOtelLoggerProvider: %w", err)
				}
			}
		}

		if degradeErr == nil {
			netloggerSvc, degradeErr = netlogger.New(netlogger.Deps{
				Mgr:                ebpfMgr,
				Bus:                bus,
				Docker:             dockerCli.APIClient,
				Cfg:                cfg,
				Domains:            handler.ReverseDNSDomains,
				OtelLoggerProvider: netloggerProvider,
				Log:                log.With("component", "netlogger"),
			})
			if degradeErr != nil {
				reason = "netlogger.New"
			}
		}

		if degradeErr == nil {
			if err := netloggerSvc.Start(watcherCtx); err != nil {
				reason = "netlogger.Start"
				degradeErr = fmt.Errorf("netlogger Start: %w", err)
				netloggerSvc = nil
			}
		}

		if degradeErr != nil {
			ev := log.Error()
			if unconfigured {
				// Unconfigured is the expected shape when running
				// without `clawker monitor up` — Warn is the right
				// level so operators don't filter the netlogger
				// event class out of triage.
				ev = log.Warn()
			}
			ev.Err(degradeErr).
				Str("event", "netlogger_unavailable").
				Str("component", "netlogger").
				Str("step", reason).
				Msg("netlogger degraded — eBPF egress events will not be exported; firewall enforcement unaffected")
			netloggerSvc = nil
			// Shut down a provider we constructed but never handed
			// off — leaving it live would leak a BatchProcessor
			// goroutine retrying a doomed export for the rest of
			// the CP lifetime. Shutdown errors are themselves
			// degraded-state telemetry: log so an operator can see
			// when teardown of a half-built provider stalls.
			if netloggerProvider != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := netloggerProvider.Shutdown(shutdownCtx); err != nil {
					log.Warn().Err(err).
						Str("event", "netlogger_provider_shutdown_failed").
						Str("component", "netlogger").
						Msg("provider Shutdown failed on degraded boot path; BatchProcessor goroutine may have leaked")
				}
				cancel()
				netloggerProvider = nil
			}
		} else {
			log.Info().
				Str("component", "netlogger").
				Str("endpoint", endpoint).
				Msg("netlogger ready — eBPF egress events exporting to OTLP")
		}
	}

	// Step 9d: periodic dns_cache garbage collection. Reclaims expired
	// entries the CoreDNS dnsbpf plugin wrote so the pinned map does not
	// grow unbounded and stale orphaned hashes do not accumulate. Runs on
	// watcherCtx so it stops on SIGTERM/drain-to-zero. Recovers per CP
	// no-panic discipline — a sweep panic must not strand the loop and
	// leave the map ungoverned.
	dnsGCCtx, dnsGCCancel := context.WithCancel(watcherCtx)
	var dnsGCWg sync.WaitGroup
	dnsGCWg.Add(1)
	go func() {
		defer dnsGCWg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).
					Str("event", "dns_gc_panic").
					Msg("dns_cache gc loop panicked — expired entries will accumulate until CP restart")
			}
		}()
		ticker := time.NewTicker(dnsGCInterval)
		defer ticker.Stop()
		for {
			select {
			case <-dnsGCCtx.Done():
				return
			case <-ticker.C:
				if n := ebpfMgr.GarbageCollectDNS(); n > 0 {
					log.Debug().Int("cleared", n).
						Str("event", "dns_gc_swept").
						Msg("dns_cache gc removed expired entries")
				}
			}
		}
	}()
	// Stop the dns_cache sweeper and wait for any in-flight sweep to finish
	// before the BPF map fd it iterates/deletes is torn down — the same
	// stop-before-teardown discipline netloggerSvc.Stop follows for the
	// ringbuf reader on the shared dns_cache map. sync.Once so the drain
	// callback (before FlushAll) and the deferred path can both call it;
	// deferred here (after ebpfMgr.Close was deferred earlier) so LIFO runs
	// this first, joining the goroutine before Close() shuts the fd.
	var stopDNSGCOnce sync.Once
	stopDNSGC := func() {
		stopDNSGCOnce.Do(func() {
			dnsGCCancel()
			dnsGCWg.Wait()
		})
	}
	defer stopDNSGC()

	listAgents := func(ctx context.Context) (int, error) {
		ids, err := listAgentIDs(ctx, listAgentsOpts{})
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
		// Stop netlogger BEFORE eBPF flush so the ringbuf reader
		// drains in-flight records before we tear down the BPF maps
		// the reader holds. Then Shutdown the OTLP provider so the
		// BatchProcessor flushes any in-flight batches to the
		// collector — netlogger.Service.Stop deliberately does not
		// touch the provider (lifetime is the caller's per
		// netlogger/CLAUDE.md), so without this Shutdown call the
		// "Stop before FlushAll" guarantee never actually flushes
		// the OTLP queue. Fresh context bounded at 5s for each:
		// the outer ctx may already be cancelled and we do not want
		// netlogger to hold up CP drain past the AgentWatcher budget.
		if netloggerSvc != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := netloggerSvc.Stop(stopCtx); err != nil {
				log.Error().Err(err).Msg("drain: netlogger stop failed")
				errs = append(errs, fmt.Errorf("netlogger stop: %w", err))
			}
			stopCancel()
		}
		if netloggerProvider != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := netloggerProvider.Shutdown(shutdownCtx); err != nil {
				log.Error().Err(err).Msg("drain: netlogger provider shutdown failed")
				errs = append(errs, fmt.Errorf("netlogger provider shutdown: %w", err))
			}
			shutdownCancel()
		}
		// Stop the dns_cache sweeper before eBPF teardown so a sweep in
		// progress can't iterate/delete a map fd that's about to close.
		stopDNSGC()
		if err := ebpfMgr.FlushAll(); err != nil {
			log.Error().Err(err).Msg("drain: ebpf flush failed")
			errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
		}
		// Stop the feeder before closing the bus so any in-flight
		// Publish lands cleanly. feederCancel is idempotent; feederDone
		// closes once the goroutine returns.
		feederCancel()
		<-feederDone
		if err := bus.Close(); err != nil {
			log.Error().Err(err).Msg("drain: overseer close failed")
			errs = append(errs, fmt.Errorf("overseer close: %w", err))
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
	// Wire the CP-driven init Executor into the dialer at construction.
	// Each new Session establish runs the static init plan against the
	// open stream. Without this, the entrypoint hangs on its fifo until
	// CLAWKER_INIT_TIMEOUT and the container fails to launch CMD.
	// Container user identity (uid/gid/username/home) lives in consts
	// and is read directly by the Executor. The plan shape is defined
	// in agent.Executor.plan() and audited by
	// TestExecutor_Plan_PrivilegeAndShape — keep the enumeration there
	// so this comment can't drift. See wireInitExecutor for the degrade
	// contract.
	initExec := wireInitExecutor(bus, log)
	dialer, err := agent.New(
		log.With("component", "agent"),
		dockerCli.APIClient,
		bus,
		agentReg,
		consts.CPClientCertPath,
		consts.CPClientKeyPath,
		caCertPool,
		initExec,
	)
	if err != nil {
		log.Error().Err(err).
			Str("event", "agent_dialer_unavailable").
			Msg("agent.dial: Dialer construction failed; CP→clawkerd command dispatch disabled. AdminService, firewall, registry, and AgentService listener continue.")
		dialer = nil
	}
	if dialer != nil {
		// Single agent startup procedure: reap orphan registry rows
		// against live docker, subscribe to container/destroy for
		// registry evict, subscribe to container/start|restart|unpause
		// for CP→clawkerd dial. Replaces the previously fragmented
		// Reap + two Subscribe wirings.
		cleanup, err := agent.Start(watcherCtx, agent.StartDeps{
			Registry: agentReg,
			DockerLister: func(ctx context.Context) ([]string, error) {
				return listAgentIDs(ctx, listAgentsOpts{All: true})
			},
			PeerLookup: agentPeerLookup,
			Dialer:     dialer,
			Bus:        bus,
			Log:        log.With("component", "agent"),
		})
		if err != nil {
			log.Error().Err(err).Msg("agent.Start failed; agent-axis subscriptions disabled")
		} else {
			agentCleanup = cleanup
		}

		// Initial path: dial every already-running agent container at
		// CP boot. Runs in its own goroutine — must NOT block CP
		// readiness, must NOT fail CP if listAgentIDs errors.
		//
		// A single failed list strands every container that was already
		// running at CP boot — those containers' ContainerStarted
		// events fired before the dockerevents subscription started, so
		// the runtime path won't pick them up either. Retry with
		// bounded backoff (3 × 100/200/400ms, mirroring the reaper's
		// listWithRetry pattern) absorbs the transient docker-daemon
		// hiccup that's the dominant failure mode at boot.
		go func() {
			// recover so a panic deep in DialAgent (cert rotation race,
			// nil deref in a future dialer change) doesn't silently
			// strand every initial-poll agent without surfacing — this
			// goroutine has no other observer. Same pattern as the
			// overseer stats heartbeat below.
			defer func() {
				if r := recover(); r != nil {
					log.Error().
						Interface("panic", r).
						Str("event", "agentdial_initial_poll_panic").
						Str("component", "cp.agentdial").
						Msg("initial agent dial goroutine panicked; initial-poll dispatch aborted (runtime ContainerStarted handlers unaffected)")
				}
			}()
			const maxAttempts = 3
			backoff := 100 * time.Millisecond
			var initialAgents []string
			var listErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				initialAgents, listErr = listAgentIDs(watcherCtx, listAgentsOpts{})
				if listErr == nil {
					if attempt > 1 {
						log.Info().Int("attempt", attempt).Str("event", "agentdial_initial_list_recovered").Msg("list agent containers recovered after retry")
					}
					break
				}
				if attempt == maxAttempts {
					break
				}
				log.Warn().Err(listErr).Int("attempt", attempt).Dur("backoff", backoff).Str("event", "agentdial_initial_list_retry").Msg("list agent containers failed; retrying")
				select {
				case <-watcherCtx.Done():
					return
				case <-time.After(backoff):
				}
				backoff *= 2
			}
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		grpcServer.GracefulStop()
	}()
	if agentServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agentServer.GracefulStop()
		}()
	}
	go func() {
		wg.Wait()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(defaultShutdownWait):
		log.Warn().Msg("gRPC graceful stop timed out, forcing")
		grpcServer.Stop()
		if agentServer != nil {
			agentServer.Stop()
		}
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
// (`host.docker.internal:4319`); the OTLP/gRPC exporter only needs
// host:port, so we strip scheme/path here.
//
// Default is TLS. Bare host:port → TLS. `https://` → TLS. Only
// explicit `http://` opts in to plaintext, so a misconfigured prod
// endpoint can't silently downgrade.
//
// mTLS material is NOT taken from env. The CP's trusted-lane
// exporter is wired in-process via internal/controlplane/otelcerts,
// which mints leaves on each handshake from the bind-mounted infra
// intermediate (see internal/controlplane/otelcerts/CLAUDE.md). Env-
// driven cert paths are deliberately rejected so a future operator
// can't smuggle in CLI-root-direct material — agent containers
// already hold CLI-root-direct leaves and could forge
// service.name=clawker-cp records on the trusted receiver if the CP
// honored an env-supplied cert path.
//
// The bridge endpoint is set up by cpboot to point at the monitor
// stack's CP-only receiver via host.docker.internal. CP is BPF-exempt
// (not enrolled in container_map) and ExtraHosts maps the gateway
// alias, so the dial reaches the host loopback published port. Agents
// on clawker-net cannot reach this endpoint AND cannot present an
// intermediate-chained client cert — two layers of isolation.
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
	return &logger.OtelOptions{
		Endpoint: endpoint,
		Insecure: insecure,
	}
}

// parseOtlpEndpoint normalises an OTEL endpoint env value to the
// host:port form `otlploggrpc.WithEndpoint` accepts, returning whether
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

// wireInitExecutor constructs the CP-driven init Executor and applies
// the degrade contract from internal/controlplane/CLAUDE.md
// ("Resilience contract — CP crashing is a security incident"):
// construction failure logs `agent_init_executor_unavailable` and
// returns nil; CP keeps running. Extracted as its own function so the
// degrade-not-crash invariant is unit-testable — see
// TestWireInitExecutor_NilBus.
func wireInitExecutor(bus *overseer.Overseer, log *logger.Logger) *agent.Executor {
	exec, err := agent.NewExecutor(bus, log.With("component", "agent.init"))
	if err == nil {
		return exec
	}
	log.Error().Err(err).
		Str("event", "agent_init_executor_unavailable").
		Msg("agent.init: Executor construction failed; CP-driven init disabled — agent containers will hang on the entrypoint fifo until timeout. CP otherwise continues.")
	return nil
}

// logHostIdentity surfaces a degraded-mode warn on the rotating CP
// logfile when the CLI didn't deliver a valid CLAWKER_HOST_UID /
// CLAWKER_HOST_GID at package-init time. CP behavior is unchanged
// — the fallback UID/GID is taken regardless — but this event is
// the operator's only triage anchor for downstream userStage
// EACCES on the ~/.claude/projects bind mount.
func logHostIdentity(log *logger.Logger, results ...consts.HostIDResolution) {
	for _, res := range results {
		if !res.Fallback {
			continue
		}
		ev := log.Warn().
			Str("event", "host_id_unavailable").
			Str("env", res.Env).
			Str("raw", res.Raw).
			Str("reason", res.Reason).
			Uint32("fallback", res.Value)
		if res.Err != nil {
			ev = ev.Err(res.Err)
		}
		ev.Msg("CP host identity env missing or invalid; userStage drops to fallback UID/GID — ~/.claude/projects bind writes may EACCES.")
	}
}
