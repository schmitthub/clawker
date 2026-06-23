package controlplane

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
	"os/signal"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/controlplane/auth"
	"github.com/schmitthub/clawker/controlplane/dockerevents"
	fwhandler "github.com/schmitthub/clawker/controlplane/firewall"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/netlogger"
	"github.com/schmitthub/clawker/controlplane/otelcerts"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/controlplane/server"
	"github.com/schmitthub/clawker/controlplane/subprocess"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type StatusCode = int

const (
	StatusOk  StatusCode = 0
	StatusErr StatusCode = 1
)

const healthCacheTTL = 2 * time.Second

// ControlPlane manages the control plane's subprocess startup
// sequence and health reporting. The /healthz endpoint actively probes
// all internal service ports — it only returns 200 when every service
// is responding.
type ControlPlane struct {
	ready   atomic.Bool
	probes  []serviceProbe
	tlsCfg  *tls.Config
	timeout time.Duration

	// Cached health state
	healthMu     sync.RWMutex
	healthOK     bool
	healthFailed string // name of first failed probe, empty if all OK
	healthAt     time.Time
}

// serviceProbe defines a TCP or HTTPS endpoint to check.
type serviceProbe struct {
	name string
	addr string
	tls  bool
}

// NewControlPlane creates a new startup orchestrator. The probes
// are configured later via SetServiceProbes once TLS config and port
// values are available.
func NewControlPlane() *ControlPlane {
	return &ControlPlane{
		timeout: 2 * time.Second,
	}
}

// SetServiceProbes configures the aggregate health probes from the
// ControlPlaneSettings. Called during CP startup after TLS config is built.
// All Ory services use HTTPS; the gRPC admin port is probed via raw TCP
// (gRPC health check would require a client).
func (o *ControlPlane) SetServiceProbes(cp config.ControlPlaneSettings, tlsCfg *tls.Config) {
	o.tlsCfg = tlsCfg
	o.probes = []serviceProbe{
		{name: "hydra-public", addr: fmt.Sprintf(consts.Localhost+":%d", cp.HydraPublicPort), tls: true},
		{name: "hydra-admin", addr: fmt.Sprintf(consts.Localhost+":%d", cp.HydraAdminPort), tls: true},
		{name: "kratos-public", addr: fmt.Sprintf(consts.Localhost+":%d", cp.KratosPublicPort), tls: true},
		{name: "kratos-admin", addr: fmt.Sprintf(consts.Localhost+":%d", cp.KratosAdminPort), tls: true},
		{name: "oathkeeper-proxy", addr: fmt.Sprintf(consts.Localhost+":%d", cp.OathkeeperPort), tls: true},
		{name: "oathkeeper-api", addr: fmt.Sprintf(consts.Localhost+":%d", cp.OathkeeperAPIPort), tls: true},
		{name: "grpc-admin", addr: fmt.Sprintf(consts.Localhost+":%d", cp.AdminPort), tls: false},
	}
}

// IsReady returns whether the CP has completed all startup steps.
func (o *ControlPlane) IsReady() bool {
	return o.ready.Load()
}

// SetReady marks the CP as ready. Called after all startup steps
// (subprocesses, eBPF load, gRPC server) have succeeded.
func (o *ControlPlane) SetReady() {
	o.ready.Store(true)
}

// HealthzHandler returns an http.Handler for the /healthz endpoint.
// Returns 200 only when SetReady was called AND all service probes pass.
// If any service is down, returns 503 with a JSON body.
func (o *ControlPlane) HealthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if !o.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"not_ready"}`)
			return
		}

		ok, failedProbe := o.cachedHealth()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"unhealthy","failed_probe":%q}`, failedProbe)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"healthy"}`)
	})
}

// cachedHealth returns the cached health state, refreshing it if the
// cache has expired. Uses double-check locking to minimize probe overhead
// under concurrent requests.
func (o *ControlPlane) cachedHealth() (bool, string) {
	o.healthMu.RLock()
	if time.Since(o.healthAt) < healthCacheTTL {
		ok, failed := o.healthOK, o.healthFailed
		o.healthMu.RUnlock()
		return ok, failed
	}
	o.healthMu.RUnlock()

	// Cache miss — probe and update.
	o.healthMu.Lock()
	defer o.healthMu.Unlock()

	// Double-check after acquiring write lock.
	if time.Since(o.healthAt) < healthCacheTTL {
		return o.healthOK, o.healthFailed
	}

	o.healthOK = true
	o.healthFailed = ""
	for _, p := range o.probes {
		if !o.probe(p) {
			o.healthOK = false
			o.healthFailed = p.name
			break
		}
	}
	o.healthAt = time.Now()
	return o.healthOK, o.healthFailed
}

// probe checks if a single service endpoint is responding.
func (o *ControlPlane) probe(p serviceProbe) bool {
	if p.tls {
		if o.tlsCfg == nil {
			return false // fail-closed: TLS required but no config
		}
		conn, err := tls.DialWithDialer(
			&net.Dialer{Timeout: o.timeout},
			"tcp", p.addr, o.tlsCfg,
		)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	conn, err := net.DialTimeout("tcp", p.addr, o.timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func Main() StatusCode {
	caCertPath := flag.String("tls-ca", consts.CPCACertPath, "CLI CA certificate")
	serverCertPath := flag.String("tls-cert", consts.CPTLSCertPath, "TLS server certificate")
	serverKeyPath := flag.String("tls-key", consts.CPTLSKeyPath, "TLS server key")
	jwkPath := flag.String("jwk", consts.CPCLIPubKeyPath, "CLI signing JWK (bind-mounted)")
	logDir := flag.String("log-dir", consts.CPLogsPath, "directory for persistent audit logs")
	flag.Parse()

	if err := run(*caCertPath, *serverCertPath, *serverKeyPath, *jwkPath, *logDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", consts.ContainerCP, err)
		return StatusErr
	}
	return StatusOk
}

// clawkercp is the containerized clawker control plane binary.
//
// It runs as the main process in the CP container, supervising Hydra,
// Oathkeeper, Kratos as subprocesses. It loads eBPF programs, serves a
// gRPC AdminService with Hydra token introspection, orchestrates the
// Docker events feeder and the typed pub/sub topics each subsystem
// produces to and consumes from, and reports readiness on /healthz.
//
// Oathkeeper runs as a subprocess for future webui HTTP auth. gRPC auth
// (CLI + agents) uses direct Hydra introspection — no Ory Go imports.
//
// The numbered startup sequence is documented in
// /controlplane/CLAUDE.md and not duplicated here so the two
// don't drift.

const (
	defaultShutdownWait = 5 * time.Second
	// cpDrainTimeout bounds the full teardown sequence (firewall stack
	// stop + eBPF flush + queue drain). Must be below the Docker SIGTERM
	// grace period (cpStopTimeout in manager/bootstrap.go = 30s) so we
	// finish before SIGKILL arrives. Envoy + CoreDNS each use Docker's
	// default 10s stop timeout, run sequentially → ~20s worst case,
	// leaving headroom.
	cpDrainTimeout = 25 * time.Second
)

// runDrainStage runs one CP drain-to-zero teardown stage under recover and
// returns nil on clean completion or a non-nil error if the stage panicked.
//
// CP §3.4 invariant: the drain sequence MUST reach ebpfMgr.FlushAll on every
// shutdown — it is the only thing that drops per-container eBPF state to zero,
// and a skipped flush leaves the next CP inheriting a frozen rule set with
// agents filtered against stale rules and no supervisor. The drain body runs
// its stages (pre-flush teardown → FlushAll → topic teardown) as a linear
// sequence of runDrainStage calls; because a panicking stage is contained here
// rather than unwinding the whole function, control always returns to the
// caller to invoke the next stage, so FlushAll is reached on any panic path.
// The structured event is the only operator triage surface (panic stacks land
// on stderr, not the rotating CP log).
func runDrainStage(log *logger.Logger, stage, event string, fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Bytes("stack", debug.Stack()).
				Str("event", event).
				Msgf("drain: %s panicked; continuing teardown so ebpf flush still runs", stage)
			err = fmt.Errorf("%s panic: %v", stage, r)
		}
	}()
	fn()
	return nil
}

// drainDeps carries the orchestrator-owned handles the drain-to-zero
// sequence acts on. These are constructed and owned by run(); the drain
// sequence only stops/closes them in the strict order below. Kept as a
// struct so run() builds the drain callback with a single call rather than
// an inline ~100-line closure, while the literal teardown ordering stays in
// one package-local function.
type drainDeps struct {
	log               *logger.Logger
	actionQueue       *fwhandler.ActionQueue
	grpcStack         *server.GRPCStack
	handler           *fwhandler.Handler
	stack             *fwhandler.Stack
	netloggerSvc      *netlogger.Service
	netloggerProvider *sdklog.LoggerProvider
	stopDNSGC         func()
	ebpfMgr           *ebpf.Manager
	feederCancel      context.CancelFunc
	feederDone        <-chan struct{}
	dockerTopic       *pubsub.Topic[dockerevents.DockerEvent]
	agentTopic        *pubsub.Topic[agent.AgentEvent]
	enrolledTopic     *pubsub.Topic[ebpf.EBPFContainerEnrolled]
}

// runDrainSequence executes the CP drain-to-zero teardown in the strict,
// un-reorderable order (INV-B2-007):
//
//  1. actionQueue.Close drains accepted submissions then rejects new ones.
//  2. grpcStack.GracefulStop refuses new RPCs, waits for in-flight handlers.
//  3. handler.CancelAllBypassTimers cancels any bypass timer mid-retry.
//  4. stack.Stop tears down the firewall stack (Envoy + CoreDNS).
//  5. netlogger Stop + provider Shutdown (BEFORE flush so the ringbuf reader
//     drains records before the BPF maps it holds are torn down).
//  6. stopDNSGC stops the dns_cache sweeper (BEFORE flush so a sweep can't
//     iterate/delete a dns_cache fd the deferred ebpfMgr.Close() is tearing
//     down).
//  7. ebpfMgr.FlushAll drops per-container eBPF state to zero.
//  8. feederCancel + topic Close.
//
// Each stage runs under runDrainStage's recover (the safe wrapper). CP §3.4
// invariant: FlushAll (stage 7) is the only thing that drops per-container
// eBPF state to zero on shutdown — if an earlier stage panicked and unwound
// the whole function, FlushAll would be skipped and the next CP would inherit
// a frozen rule set with agents filtered against stale rules and no
// supervisor. Containing each stage's panic to that stage lets the linear
// sequence fall through to FlushAll on any panic path. Errors are aggregated
// so a broken drain exits non-zero.
func runDrainSequence(ctx context.Context, d drainDeps) error {
	var errs []error
	safe := func(stage, event string, fn func()) {
		if err := runDrainStage(d.log, stage, event, fn); err != nil {
			errs = append(errs, err)
		}
	}

	safe("pre-flush teardown", "drain_preflush_panic", func() {
		if err := d.actionQueue.Close(); err != nil {
			d.log.Warn().Err(err).Msg("actionQueue close failed")
		}
		d.grpcStack.GracefulStop(ctx)
		d.handler.CancelAllBypassTimers()
		if err := d.stack.Stop(ctx); err != nil {
			d.log.Error().Err(err).Msg("drain: firewall stack stop failed")
			errs = append(errs, fmt.Errorf("stack stop: %w", err))
		}
		// Stop netlogger BEFORE eBPF flush so the ringbuf reader drains
		// in-flight records before we tear down the BPF maps the reader holds.
		// Then Shutdown the OTLP provider so the BatchProcessor flushes
		// in-flight batches — netlogger.Service.Stop deliberately does not
		// touch the provider. Fresh 5s contexts: the outer ctx may already be
		// cancelled and netlogger must not hold up CP drain past the
		// AgentWatcher budget.
		if d.netloggerSvc != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := d.netloggerSvc.Stop(stopCtx); err != nil {
				d.log.Error().Err(err).Msg("drain: netlogger stop failed")
				errs = append(errs, fmt.Errorf("netlogger stop: %w", err))
			}
			stopCancel()
		}
		if d.netloggerProvider != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := d.netloggerProvider.Shutdown(shutdownCtx); err != nil {
				d.log.Error().Err(err).Msg("drain: netlogger provider shutdown failed")
				errs = append(errs, fmt.Errorf("netlogger provider shutdown: %w", err))
			}
			shutdownCancel()
		}
		d.stopDNSGC()
	})

	// Flush per-container eBPF state. Reached unconditionally — a panic in any
	// pre-flush step was contained above, so this always runs.
	safe("ebpf flush", "drain_ebpf_flush_panic", func() {
		if err := d.ebpfMgr.FlushAll(); err != nil {
			d.log.Error().Err(err).Msg("drain: ebpf flush failed")
			errs = append(errs, fmt.Errorf("ebpf flush: %w", err))
		}
	})

	// Stop the feeder before closing the topics so any in-flight Publish lands
	// cleanly, then close every topic so subscriber drain goroutines exit.
	// feederCancel is idempotent; feederDone closes once the goroutine returns.
	safe("feeder/topic teardown", "drain_topic_teardown_panic", func() {
		d.feederCancel()
		<-d.feederDone
		for _, t := range []struct {
			name  string
			close func() error
		}{
			{"docker", d.dockerTopic.Close},
			{"agent", d.agentTopic.Close},
			{"ebpf_enrolled", d.enrolledTopic.Close},
		} {
			if err := t.close(); err != nil {
				d.log.Error().Err(err).Str("topic", t.name).Msg("drain: topic close failed")
				errs = append(errs, fmt.Errorf("%s topic close: %w", t.name, err))
			}
		}
	})

	return errors.Join(errs...)
}

// startOryStack the Ory auth stack (Kratos, Hydra, Oathkeeper) —
// startup GATE 1. NewOryStack builds the single CLI CA pool + caTLS up front;
// Start runs the Ory choreography. It returns the single CA surface
// (caCertPool, caTLS) every downstream consumer reuses — never rebuilt — and
// configures the orchestrator's aggregate /healthz service probes. A failure
// fails CP startup (pre-SetReady, code 1) WITHOUT an eBPF flush.
func startOryStack(ctx context.Context, cfg config.Config, subMgr *subprocess.SubprocessManager, orchestrator *ControlPlane, cp config.ControlPlaneSettings, caCertPath, jwkPath string, log *logger.Logger) (*x509.CertPool, *tls.Config, error) {
	oryStack, err := auth.NewOryStack(cfg, subMgr, caCertPath, jwkPath, log)
	if err != nil {
		return nil, nil, fmt.Errorf("ory stack: %w", err)
	}
	if err := oryStack.Start(ctx); err != nil {
		return nil, nil, err
	}
	caCertPool := oryStack.CACertPool()
	caTLS := oryStack.CATLS()
	// /healthz actively probes ALL service ports — 200 only when every responds.
	orchestrator.SetServiceProbes(cp, caTLS)
	return caCertPool, caTLS, nil
}

// buildAgentInfra builds the durable sqlite agent registry (CP is the
// SOLE writer; EnsureSchema before NewSQLiteWriter so its SELECT COUNT sees the
// table), the peer-IP→Docker→labels resolver (grounds identity trust on a
// kernel-attested source, not cert claims), the managed-agent ContainerLister,
// and the agent worldview Repository. It wires the Repository's two
// subscriptions (agent topic per-container view + dockerevents evict-on-rm) —
// the load-bearing projection; the heartbeat only reads its Len().
func buildAgentInfra(log *logger.Logger, dockerCli *docker.Client, cfg config.Config, agentTopic *pubsub.Topic[agent.AgentEvent], dockerTopic *pubsub.Topic[dockerevents.DockerEvent]) (
	agentReg agent.Registry,
	agentPeerLookup *agent.MobyPeerLookup,
	lister *agent.ContainerLister,
	agentRepo *agent.Repository,
	err error,
) {
	if err := agent.EnsureSchema(consts.CPControlPlaneDBPath, log.With("component", "agent")); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("agent ensure schema: %w", err)
	}
	agentReg, err = agent.NewSQLiteWriter(consts.CPControlPlaneDBPath, log.With("component", "agent"))
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("agent sqlite: %w", err)
	}
	agentPeerLookup = agent.NewMobyPeerLookup(dockerCli.APIClient, log.With("component", "agent-peer-lookup"))
	// ListOpts{} = running only (handler + watcher); ListOpts{All:true} =
	// running + stopped (reaper + dial reconciler).
	lister = agent.NewContainerLister(dockerCli.APIClient, cfg)

	agentRepo = agent.NewRepository()
	agentRepo.Subscribe(agentTopic)
	agentRepo.SubscribeDockerEvents(dockerTopic)
	return agentReg, agentPeerLookup, lister, agentRepo, nil
}

// startHealthz serves the /healthz endpoint (the orchestrator's
// aggregate readiness + service-probe surface) on HealthPort in a goroutine,
// returning the *http.Server so run()'s shutdown sequence can GracefulStop it.
// A non-ErrServerClosed listen failure is deposited on serveFailed so the serve
// select tears down.
func startHealthz(cp config.ControlPlaneSettings, log *logger.Logger, orchestrator *ControlPlane, serveFailed chan error) *http.Server {
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
	return healthServer
}

// firewallBringupGate is the settings-driven firewall bringup startup
// GATE. When firewall.enable (settings.yaml) is set, the stack must be up
// whenever CP is — not only on a CLI FirewallInit — so the same queued
// FirewallInit runs synchronously BEFORE SetReady, making a green /healthz mean
// "everything settings enable is enforcing". A failure returns an error that
// fails CP startup (pre-SetReady, code 1) WITHOUT an eBPF flush — enrolled
// agents stay fail-closed against pinned state. The structured
// event=firewall_bringup_failed line is the operator's only triage surface (the
// exit itself lands on stderr/docker logs).
func firewallBringupGate(cfg config.Config, log *logger.Logger, handler *fwhandler.Handler) error {
	if !cfg.Settings().Firewall.FirewallEnabled() {
		return nil
	}
	log.Info().Str("component", "firewall-bringup").
		Msg("firewall bringup: starting stack (settings firewall.enable)")
	if _, err := handler.FirewallInit(context.Background(), &adminv1.FirewallInitRequest{}); err != nil {
		log.Error().Err(err).
			Str("event", "firewall_bringup_failed").
			Str("component", "firewall-bringup").
			Msg("firewall bringup failed — CP exiting (startup gate); enrolled agents stay fail-closed against pinned eBPF state; inspect docker logs and re-run `clawker controlplane up`")
		return fmt.Errorf("firewall bringup: %w", err)
	}
	log.Info().Str("component", "firewall-bringup").Msg("firewall stack up")
	return nil
}

// startFeeder constructs the dockerevents feeder — the sole
// producer of DockerEvent — and launches its Supervise loop on a child of
// watcherCtx. feederCancel is separate from watcherCtx so the drain sequence
// can stop the feeder BEFORE closing topics (avoids dropped-publish noise);
// feederDone closes once the Supervise goroutine returns. Supervise wraps Run
// with cancel-vs-error discrimination and the serveFailed send.
func startFeeder(watcherCtx context.Context, cfg config.Config, log *logger.Logger, dockerCli *docker.Client, dockerTopic *pubsub.Topic[dockerevents.DockerEvent], serveFailed chan error) (context.CancelFunc, <-chan struct{}, error) {
	feeder, err := dockerevents.New(dockerCli.APIClient, dockerTopic, dockerevents.Options{
		ManagedLabelKey:   cfg.LabelManaged(),
		ManagedLabelValue: cfg.ManagedLabelValue(),
		Logger:            log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("dockerevents feeder: %w", err)
	}
	feederCtx, feederCancel := context.WithCancel(watcherCtx)
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		feeder.Supervise(feederCtx, serveFailed)
	}()
	return feederCancel, feederDone, nil
}

// buildEnforcement builds the Docker client, cgroup-driver detection +
// container resolver, the firewall rules store + Stack, and the eBPF load +
// defensive stale-bypass cleanup. The eBPF Load and CleanupStaleBypass are
// pre-SetReady startup GATES — any error returned here propagates out of run()
// BEFORE SetReady (exit code 1) WITHOUT an eBPF flush, so agents enrolled by a
// previous CP stay fail-closed against pinned state.
//
// The returned cleanup closes the Docker client and the eBPF manager; run()
// joins its error into retErr so the on-failure restart policy investigates a
// partial teardown rather than silently blessing it.
func buildEnforcement(ctx context.Context, cfg config.Config, log *logger.Logger, otelCerts fwhandler.OtelCertProvisioner) (
	dockerCli *docker.Client,
	containerResolver fwhandler.ContainerResolver,
	rulesStore *storage.Store[fwhandler.EgressRulesFile],
	stack *fwhandler.Stack,
	ebpfMgr *ebpf.Manager,
	cleanup func() error,
	err error,
) {
	// Docker client serves the container resolver, the firewall stack (Envoy +
	// CoreDNS siblings over DooD), and the AgentWatcher poll loop.
	dockerCli, err = docker.NewClient(ctx, cfg, log)
	if err != nil {
		return nil, nil, nil, nil, nil, func() error { return nil }, fmt.Errorf("docker client: %w", err)
	}
	cleanup = func() error { dockerCli.Close(); return nil }

	// Cgroup driver queried once and cached on the resolver (BPF cgroup paths
	// come from firewall.EBPFCgroupPath, the single source of truth).
	cgroupDriver, err := fwhandler.DetectCgroupDriver(ctx, dockerCli)
	if err != nil {
		return dockerCli, nil, nil, nil, nil, cleanup, fmt.Errorf("cgroup driver: %w", err)
	}
	log.Info().Str("cgroup_driver", cgroupDriver).Msg("Docker cgroup driver detected")
	containerResolver = fwhandler.NewContainerResolver(dockerCli, cgroupDriver)

	// Firewall stack handle. Host bootstrap owns EnsureRunning; the drain path
	// owns Stop. Degraded mode (nil otelCerts) was logged as
	// event=otelcerts_unavailable in bootLogging.
	rulesStore, err = fwhandler.NewRulesStore(cfg)
	if err != nil {
		return dockerCli, containerResolver, nil, nil, nil, cleanup, fmt.Errorf("rules store: %w", err)
	}
	stack = fwhandler.NewStack(dockerCli, cfg, log, rulesStore, otelCerts)

	ebpfMgr = ebpf.NewManager(log)
	if err := ebpfMgr.Load(); err != nil {
		return dockerCli, containerResolver, rulesStore, stack, nil, cleanup, fmt.Errorf("ebpf load: %w", err)
	}
	// Extend cleanup to also close the eBPF manager (join its error so a partial
	// teardown is investigated, not silently blessed).
	cleanup = func() error {
		var errs []error
		if cErr := ebpfMgr.Close(); cErr != nil {
			log.Error().Err(cErr).Msg("ebpf close error")
			errs = append(errs, fmt.Errorf("ebpf close: %w", cErr))
		}
		dockerCli.Close()
		return errors.Join(errs...)
	}
	log.Info().Msg("eBPF programs loaded")

	// Defensive startup cleanup (INV-B2-013): cgroup IDs are reusable across
	// container generations, so a leftover bypass_map entry from a crashed
	// previous CP could grant a fresh unrelated container unrestricted egress.
	cleared, err := ebpfMgr.CleanupStaleBypass()
	if err != nil {
		return dockerCli, containerResolver, rulesStore, stack, ebpfMgr, cleanup, fmt.Errorf("defensive bypass cleanup: %w", err)
	}
	if cleared > 0 {
		log.Info().Int("cleared", cleared).Msg("defensive startup: cleared stale bypass_map entries")
	}
	return dockerCli, containerResolver, rulesStore, stack, ebpfMgr, cleanup, nil
}

// grpcStackDeps carries the handles buildGRPCStack builds the firewall
// handler + gRPC servers against. caTLS/caCertPool are the single CA
// surface from the Ory stack — never rebuilt.
type grpcStackDeps struct {
	log               *logger.Logger
	cfg               config.Config
	ebpfMgr           *ebpf.Manager
	stack             *fwhandler.Stack
	rulesStore        *storage.Store[fwhandler.EgressRulesFile]
	containerResolver fwhandler.ContainerResolver
	agentReg          agent.Registry
	agentPeerLookup   *agent.MobyPeerLookup
	lister            *agent.ContainerLister
	enrolledTopic     *pubsub.Topic[ebpf.EBPFContainerEnrolled]
	caCertPool        *x509.CertPool
	caTLS             *tls.Config
	cp                config.ControlPlaneSettings
	serverCertPath    string
	serverKeyPath     string
}

// buildGRPCStack constructs the firewall ActionQueue (the single-goroutine FIFO
// worker every Firewall* RPC runs through — drain step 1, injected via
// HandlerDeps), the firewall Handler, and the gRPC stack (admin always up;
// agent listener up only when the IdentityInterceptor resolves), then starts
// serving on the buffered serveFailed channel. It returns those handles plus a
// belt-and-braces cleanup that closes the ActionQueue on non-drain exit paths.
func buildGRPCStack(d grpcStackDeps) (
	actionQueue *fwhandler.ActionQueue,
	handler *fwhandler.Handler,
	grpcStack *server.GRPCStack,
	serveFailed chan error,
	cleanup func(),
	err error,
) {
	actionQueue = fwhandler.NewActionQueue(d.log)
	cleanup = func() {
		if err := actionQueue.Close(); err != nil {
			d.log.Warn().Err(err).Msg("actionQueue close failed")
		}
	}

	// Handler holds the publish-only enrolledTopic (FirewallEnable publishes
	// EBPFContainerEnrolled; netlogger subscribes to hydrate its label cache).
	handler, err = fwhandler.NewHandler(fwhandler.HandlerDeps{
		EBPF:          d.ebpfMgr,
		Stack:         d.stack,
		Store:         d.rulesStore,
		Cfg:           d.cfg,
		Resolver:      d.containerResolver,
		Log:           d.log,
		Queue:         actionQueue,
		EnrolledTopic: d.enrolledTopic,
		ListAgents:    func(ctx context.Context) ([]string, error) { return d.lister.List(ctx, agent.ListOpts{}) },
	})
	if err != nil {
		return actionQueue, nil, nil, nil, cleanup, fmt.Errorf("firewall handler: %w", err)
	}

	grpcStack, err = server.NewGRPCStack(server.GRPCDeps{
		Handler:        handler,
		Registry:       d.agentReg,
		PeerLookup:     d.agentPeerLookup,
		ServerCertPath: d.serverCertPath,
		ServerKeyPath:  d.serverKeyPath,
		CACertPool:     d.caCertPool,
		CATLS:          d.caTLS,
		HydraAdminPort: d.cp.HydraAdminPort,
		AdminPort:      d.cp.AdminPort,
		AgentPort:      d.cp.AgentPort,
		Log:            d.log,
	})
	if err != nil {
		return actionQueue, handler, nil, nil, cleanup, fmt.Errorf("grpc stack: %w", err)
	}

	// Buffered(4) so gRPC admin/agent, healthz, or the feeder can deposit a
	// failure without blocking before the serve select is reached.
	serveFailed = make(chan error, 4)
	grpcStack.Serve(serveFailed)
	return actionQueue, handler, grpcStack, serveFailed, cleanup, nil
}

// newDrainCallback wraps runDrainSequence in a sync.Once so SIGTERM and the
// AgentWatcher's drain-to-zero converge on a single teardown — whichever
// trigger wins runs the literal un-reorderable drain order (reaching
// ebpfMgr.FlushAll under per-stage recover) exactly once. Every later call,
// including run()'s final `return drainCallback(...)`, returns the captured
// error without re-running the teardown.
func newDrainCallback(deps drainDeps) func(context.Context) error {
	var (
		drainOnce sync.Once
		drainErr  error
	)
	return func(ctx context.Context) error {
		drainOnce.Do(func() { drainErr = runDrainSequence(ctx, deps) })
		return drainErr
	}
}

// bootLogging stands up the trusted-lane OTel cert provisioner and the
// process logger, returning the concrete *otelcerts.Service (for the post-logger
// SetLogger + netlogger wiring), the fwhandler.OtelCertProvisioner interface (for
// the firewall stack — UNTYPED nil on failure so the stack's `== nil` check fires;
// never a typed-nil boxed into the interface), and a cleanup closure run() defers
// to flush the logger on every return arm.
//
// LANDMINE: on cert-provisioner failure NewCPProvisioner returns literal nils for
// all three values, and on ANY cert failure OtelOptions is dropped entirely (not
// just the TLSConfig) so the logger never half-inits mTLS with no creds nor
// plaintext-falls-back to the untrusted lane. The structured otelcerts/logger
// degraded-event lines are emitted here (after the logger exists) so they land on
// the structured surface, not stderr.
func bootLogging(logDir string) (log *logger.Logger, otelCertsSvc *otelcerts.Service, otelCerts fwhandler.OtelCertProvisioner, cleanup func(), err error) {
	// Build the OTel cert provisioner BEFORE logger.New — the logger's OTLP
	// exporter locks in its TLSConfig at construction (no hot-reconfig hook).
	otelCertsSvc, otelCerts, otelTLSConfig, otelCertsErr := otelcerts.NewCPProvisioner(nil)

	otelOpts := logger.OtelOptionsFromEnv()
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
	log, loggerInitErr := logger.New(loggerOpts)
	if loggerInitErr != nil {
		// Fall back to stderr-only if log dir isn't writable.
		log = logger.NewWriter(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s: warning: file logging unavailable (%v), using stderr only\n", consts.ContainerCP, loggerInitErr)
	}
	log = log.With("component", consts.ContainerCP)

	// log.Close rides a fresh base context (not the signal context) so the
	// final flush survives the shutdown signal; idempotent + deferred by run(),
	// it covers every return arm.
	cleanup = func() {
		// Surface a Close failure on stderr (→ docker logs), NOT into retErr:
		// the OTEL mirror is self-healing and CP's exit code is a
		// teardown-integrity signal — a benign telemetry outage must not trip
		// the on-failure restart policy.
		if cErr := log.Close(context.Background()); cErr != nil {
			fmt.Fprintf(os.Stderr, "%s: logger close failed: %v\n", consts.ContainerCP, cErr)
		}
	}
	log.Info().Msg("starting")

	// Wire the real logger in now (LoadTLSConfig reads s.log lazily at
	// handshake time, so post-construction SetLogger is safe).
	if otelCertsSvc != nil {
		otelCertsSvc.SetLogger(log)
	}

	if loggerInitErr != nil {
		// Stderr-only fallback also loses the OTel bridge (NewWriter takes no
		// OtelOptions) — operators must know the trusted lane is dark.
		log.Error().Err(loggerInitErr).
			Str("event", "file_logger_unavailable").
			Str("component", "logger").
			Msg("file logging init failed — running with stderr-only logger, OTel bridge disabled")
	}

	if otelCertsErr != nil {
		log.Error().Err(otelCertsErr).
			Str("event", "otelcerts_unavailable").
			Str("component", "otelcerts").
			Msg("trusted-lane OTLP push disabled — CP and infra services run with file/stderr logs only")
	}

	logHostIdentity(log, consts.HostUIDResolution(), consts.HostGIDResolution())
	return log, otelCertsSvc, otelCerts, cleanup, nil
}

// buildTopics constructs the three typed pub/sub topics (one per payload
// type). The pipe holds zero state — each subscriber projects events into its
// own private store — and the single generic audit hook self-attaches
// inside NewTopic, so the orchestrator wires ZERO hooks. A topic build failure
// fails CP startup (pre-SetReady): it is a wiring regression, not a degraded
// subsystem.
func buildTopics(busLog *logger.Logger) (
	dockerTopic *pubsub.Topic[dockerevents.DockerEvent],
	agentTopic *pubsub.Topic[agent.AgentEvent],
	enrolledTopic *pubsub.Topic[ebpf.EBPFContainerEnrolled],
	err error,
) {
	if dockerTopic, err = pubsub.NewTopic[dockerevents.DockerEvent](busLog, pubsub.WithBuffer(2048)); err != nil {
		return nil, nil, nil, fmt.Errorf("docker events topic: %w", err)
	}
	if agentTopic, err = pubsub.NewTopic[agent.AgentEvent](busLog, pubsub.WithBuffer(2048)); err != nil {
		return nil, nil, nil, fmt.Errorf("agent events topic: %w", err)
	}
	if enrolledTopic, err = pubsub.NewTopic[ebpf.EBPFContainerEnrolled](busLog, pubsub.WithBuffer(2048)); err != nil {
		return nil, nil, nil, fmt.Errorf("ebpf enrolled topic: %w", err)
	}
	return dockerTopic, agentTopic, enrolledTopic, nil
}

// workerDeps carries the handles the long-lived observability workers
// (pub/sub stats heartbeat, netlogger, dns_cache GC) are built against.
type workerDeps struct {
	log           *logger.Logger
	busLog        *logger.Logger
	cfg           config.Config
	ebpfMgr       *ebpf.Manager
	dockerCli     *docker.Client
	otelCertsSvc  *otelcerts.Service
	handler       *fwhandler.Handler
	agentRepo     *agent.Repository
	dockerTopic   *pubsub.Topic[dockerevents.DockerEvent]
	agentTopic    *pubsub.Topic[agent.AgentEvent]
	enrolledTopic *pubsub.Topic[ebpf.EBPFContainerEnrolled]
}

// startWorkers launches the three long-lived observability workers on
// watcherCtx and returns the netlogger service + its caller-owned provider +
// the dns_cache GC stop func — the handles the drain sequence acts on. All
// three recover internally per the CP no-panic discipline; the heartbeat and
// GC are cancelled transitively when run() cancels watcherCtx, and the drain
// sequence stops netlogger + GC explicitly before FlushAll.
//
// netlogger degrades to a nil service (drain skips Stop) on any failure with an
// event=netlogger_unavailable line; the returned provider is caller-owned (the
// drain sequence Shutdowns it — Service.Stop does not).
func startWorkers(watcherCtx context.Context, d workerDeps) (*netlogger.Service, *sdklog.LoggerProvider, func()) {
	// pub/sub stats heartbeat — coarse per-topic + agent-worldview
	// health signal on the CP log (worldview size via the Repository's Len()).
	pubsub.NewStatsHeartbeat(d.busLog, pubsub.DefaultStatsInterval,
		pubsub.NewTopicStatsSource("docker", d.dockerTopic.Stats),
		pubsub.NewTopicStatsSource("agent", d.agentTopic.Stats),
		pubsub.NewTopicStatsSource("ebpf_enrolled", d.enrolledTopic.Stats),
		pubsub.NewWorldviewStatsSource("agent", d.agentRepo.Agents.Len),
	).Start(watcherCtx)

	// netlogger — drains the BPF per-decision ringbuf to the
	// trusted-infra OTLP receiver.
	netloggerSvc, netloggerProvider := netlogger.Start(watcherCtx, netlogger.StartDeps{
		Cfg:           d.cfg,
		Log:           d.log,
		Mgr:           d.ebpfMgr,
		Docker:        d.dockerCli.APIClient,
		OtelCerts:     d.otelCertsSvc,
		EnrolledTopic: d.enrolledTopic,
		EvictTopic:    d.dockerTopic,
		Domains:       d.handler.ReverseDNSDomains,
	})

	// dns_cache GC — reclaims expired entries the CoreDNS dnsbpf
	// plugin wrote so the pinned map stays bounded. The sync.Once-guarded stop
	// is called BOTH from the drain body (before FlushAll) AND deferred by run()
	// (LIFO before ebpfMgr.Close), so an in-flight sweep is joined before the
	// dns_cache fd is torn down.
	stopDNSGC := ebpf.NewDNSGarbageCollector(d.ebpfMgr, d.log, ebpf.DNSGCOpts{}).Start(watcherCtx)

	return netloggerSvc, netloggerProvider, stopDNSGC
}

// agentDialerDeps carries the handles startAgentDialer needs, grouped into
// a struct so its signature stays one call rather than an 8-arg sprawl.
type agentDialerDeps struct {
	log         *logger.Logger
	dockerCli   *docker.Client
	agentTopic  *pubsub.Topic[agent.AgentEvent]
	dockerTopic *pubsub.Topic[dockerevents.DockerEvent]
	agentReg    agent.Registry
	peerLookup  *agent.MobyPeerLookup
	lister      *agent.ContainerLister
	caCertPool  *x509.CertPool
}

// startAgentDialer wires the CP-driven init Executor, the CP→clawkerd Dialer,
// the agent-axis subscriptions, and the boot-time dial of every already-running
// agent. It returns a cleanup closure run() defers (no-op until agent.Start
// succeeds, so deferring it is always safe).
//
// CP §3.4 degrade contract: a broken init Executor → initExec=nil (entrypoint
// fifo timeout is the only user-visible effect); a broken dialer → dialer=nil
// (CP→clawkerd dispatch disabled). Neither cascades — AdminService, firewall,
// registry, and the AgentService listener all stay up. Every degrade emits its
// own event=<subsystem>_unavailable line via wireInitExecutor / agent.NewDialer.
func startAgentDialer(watcherCtx context.Context, d agentDialerDeps) func() {
	agentCleanup := func() {}

	// The CP-driven init Executor runs the static init plan on each new
	// Session; without it the entrypoint hangs on its fifo until
	// CLAWKER_INIT_TIMEOUT. wireInitExecutor holds the degrade contract.
	initExec := wireInitExecutor(d.agentTopic, d.dockerCli, d.log)
	dialer, err := agent.NewDialer(
		d.log.With("component", "agent"),
		d.dockerCli.APIClient,
		d.agentTopic,
		d.agentReg,
		consts.CPClientCertPath,
		consts.CPClientKeyPath,
		d.caCertPool,
		initExec,
	)
	if err != nil {
		d.log.Error().Err(err).
			Str("event", "agent_dialer_unavailable").
			Msg("agent.dial: Dialer construction failed; CP→clawkerd command dispatch disabled. AdminService, firewall, registry, and AgentService listener continue.")
		dialer = nil
	}
	if dialer == nil {
		return agentCleanup
	}

	// agent.Start reaps orphan registry rows against live docker and
	// subscribes to dockerTopic for evict / session-cancel / dial.
	cleanup, err := agent.Start(watcherCtx, agent.StartDeps{
		Registry: d.agentReg,
		DockerLister: func(ctx context.Context) ([]string, error) {
			return d.lister.List(ctx, agent.ListOpts{All: true})
		},
		PeerLookup:  d.peerLookup,
		Dialer:      dialer,
		DockerTopic: d.dockerTopic,
		AgentTopic:  d.agentTopic,
		Log:         d.log.With("component", "agent"),
	})
	if err != nil {
		d.log.Error().Err(err).Msg("agent.Start failed; agent-axis subscriptions disabled")
	} else {
		agentCleanup = cleanup
	}

	// Dial every already-running agent at boot. DialAllRunning spawns its
	// own recovered goroutine with bounded backoff — non-blocking and never
	// fails CP if the list errors.
	dialer.DialAllRunning(watcherCtx, d.lister, agent.ListOpts{})
	return agentCleanup
}

func run(caCertPath, serverCertPath, serverKeyPath, jwkPath, logDir string) (retErr error) {
	// trusted-lane OTel certs + logger (see bootLogging).
	log, otelCertsSvc, otelCerts, logCleanup, err := bootLogging(logDir)
	if err != nil {
		return err
	}
	// Base context for the run, parent of signalCtx; logCleanup rides its own
	// base context so the final flush survives the shutdown signal. Deferred
	// here so it covers every return arm.
	ctx := context.Background()
	defer logCleanup()

	// config + signal-aware contexts. All ports come from
	// settings.ControlPlane (config dir set by CLAWKER_CONFIG_DIR).
	cfg, err := config.NewConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cp := cfg.Settings().ControlPlane

	subMgr := subprocess.NewSubprocessManager(log)
	orchestrator := NewControlPlane()

	// Signal-aware context for startup + serve (SIGTERM = `docker stop`, CP is
	// PID 1; SIGINT = dev runs). SIGKILL is the supervisor's uncatchable
	// backstop.
	signalCtx, signalStop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer signalStop()

	// signalCtx hides which signal fired; this buffered(1) channel records the
	// value for the shutdown log line. Cancellation is still owned by signalCtx.
	sigValueCh := make(chan os.Signal, 1)
	signal.Notify(sigValueCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigValueCh)

	// watcherCtx is the lifetime of all long-lived workers (feeder, netlogger,
	// dns GC, agent watcher, dial reconciler); SIGTERM and drain both cancel it.
	watcherCtx, watcherCancel := context.WithCancel(ctx)
	defer watcherCancel()

	// Ory auth stack (Kratos, Hydra, Oathkeeper) — startup GATE 1 (see
	// startOryStack). caCertPool/caTLS are the single CA surface reused
	// everywhere downstream; never rebuilt.
	caCertPool, caTLS, err := startOryStack(signalCtx, cfg, subMgr, orchestrator, cp, caCertPath, jwkPath, log)
	if err != nil {
		return err
	}

	// Docker client + firewall stack + eBPF load — startup GATES (see
	// buildEnforcement; ebpf Load + CleanupStaleBypass are pre-SetReady gates
	// that exit 1 without flush). enforcementCleanup closes the eBPF manager
	// (error joined into retErr) then the Docker client, on every return arm.
	dockerCli, containerResolver, rulesStore, stack, ebpfMgr, enforcementCleanup, err := buildEnforcement(signalCtx, cfg, log, otelCerts)
	// Register cleanup before the error check: buildEnforcement returns a
	// non-nil cleanup even on a mid-construction failure (e.g. cgroup-driver
	// detect fails after the Docker client opened), so a partial build still
	// closes what it opened.
	defer func() { retErr = errors.Join(retErr, enforcementCleanup()) }()
	if err != nil {
		return err
	}

	// typed pub/sub topics (see buildTopics). One topic per payload type;
	// the single generic audit hook self-attaches inside NewTopic.
	busLog := log.With("component", "pubsub")
	dockerTopic, agentTopic, enrolledTopic, err := buildTopics(busLog)
	if err != nil {
		return err
	}

	// Belt-and-braces topic close for non-drain exit paths; the drain callback
	// closes them explicitly first (after every producer/worker is cancelled).
	defer func() {
		for _, t := range []interface{ Close() error }{dockerTopic, agentTopic, enrolledTopic} {
			if err := t.Close(); err != nil {
				log.Warn().Err(err).Msg("topic close failed")
			}
		}
	}()

	// agent registry (sqlite) + peer lookup + lister + worldview
	// Repository with its two subscriptions wired — see buildAgentInfra.
	agentReg, agentPeerLookup, lister, agentRepo, err := buildAgentInfra(log, dockerCli, cfg, agentTopic, dockerTopic)
	if err != nil {
		return err
	}

	// firewall handler + gRPC servers (admin + agent listeners) — see
	// buildGRPCStack. The ActionQueue Close is drain step 1 (injected via
	// HandlerDeps); grpcCleanup is the belt-and-braces close for non-drain
	// exit paths.
	actionQueue, handler, grpcStack, serveFailed, grpcCleanup, err := buildGRPCStack(grpcStackDeps{
		log:               log,
		cfg:               cfg,
		ebpfMgr:           ebpfMgr,
		stack:             stack,
		rulesStore:        rulesStore,
		containerResolver: containerResolver,
		agentReg:          agentReg,
		agentPeerLookup:   agentPeerLookup,
		lister:            lister,
		enrolledTopic:     enrolledTopic,
		caCertPool:        caCertPool,
		caTLS:             caTLS,
		cp:                cp,
		serverCertPath:    serverCertPath,
		serverKeyPath:     serverKeyPath,
	})
	if err != nil {
		return err
	}
	defer grpcCleanup()

	// settings-driven firewall bringup — startup GATE, pre-SetReady
	// (see firewallBringupGate). A failure exits 1 without an eBPF flush.
	if err := firewallBringupGate(cfg, log, handler); err != nil {
		return err
	}

	orchestrator.SetReady()

	// /healthz server (see startHealthz). Returns the server so the
	// shutdown sequence can GracefulStop it.
	healthServer := startHealthz(cp, log, orchestrator, serveFailed)

	// dockerevents feeder — the sole producer of DockerEvent (see
	// startFeeder). feederCancel is the drain sequence's stop-before-topic-close
	// handle; deferred here as belt-and-braces for non-drain exit.
	feederCancel, feederDone, err := startFeeder(watcherCtx, cfg, log, dockerCli, dockerTopic, serveFailed)
	if err != nil {
		return err
	}
	defer feederCancel()

	// long-lived observability workers (stats heartbeat, netlogger,
	// dns_cache GC) — see startWorkers. They run on watcherCtx, so
	// run()'s watcherCancel stops the heartbeat and GC transitively; the drain
	// sequence stops netlogger + GC explicitly before FlushAll. The deferred
	// stopDNSGC is belt-and-braces (LIFO before ebpfMgr.Close) so an in-flight
	// sweep is joined before the dns_cache fd is torn down.
	netloggerSvc, netloggerProvider, stopDNSGC := startWorkers(watcherCtx, workerDeps{
		log:           log,
		busLog:        busLog,
		cfg:           cfg,
		ebpfMgr:       ebpfMgr,
		dockerCli:     dockerCli,
		otelCertsSvc:  otelCertsSvc,
		handler:       handler,
		agentRepo:     agentRepo,
		dockerTopic:   dockerTopic,
		agentTopic:    agentTopic,
		enrolledTopic: enrolledTopic,
	})
	defer stopDNSGC()

	// drain-to-zero teardown callback. newDrainCallback wraps
	// runDrainSequence in sync.Once so SIGTERM and the AgentWatcher's
	// drain-to-zero converge on the same teardown — whichever trigger wins runs
	// it exactly once; every later call returns the captured error, so run()'s
	// final `return drainCallback(...)` reports the drain outcome without
	// re-running it.
	listAgents := func(ctx context.Context) (int, error) {
		ids, err := lister.List(ctx, agent.ListOpts{})
		if err != nil {
			return 0, err
		}
		return len(ids), nil
	}
	drainCallback := newDrainCallback(drainDeps{
		log:               log,
		actionQueue:       actionQueue,
		grpcStack:         grpcStack,
		handler:           handler,
		stack:             stack,
		netloggerSvc:      netloggerSvc,
		netloggerProvider: netloggerProvider,
		stopDNSGC:         stopDNSGC,
		ebpfMgr:           ebpfMgr,
		feederCancel:      feederCancel,
		feederDone:        feederDone,
		dockerTopic:       dockerTopic,
		agentTopic:        agentTopic,
		enrolledTopic:     enrolledTopic,
	})

	// agent watcher (drain-to-zero trigger).
	watcher, err := agent.NewAgentWatcher(log, listAgents, drainCallback, agent.AgentWatcherOptions{})
	if err != nil {
		return fmt.Errorf("agent watcher: %w", err)
	}
	watcherDone := make(chan error, 1)
	go func() {
		// CP no-panic discipline: this goroutine owns the drain-to-zero
		// teardown (drainCallback runs ebpfMgr.FlushAll). A panic in watcher.Run
		// must NOT unwind past the channel send, or watcherDone never receives,
		// the serve select never drains, and PID 1 dies with eBPF pinned and
		// unsupervised (§3.4). Convert any panic into a terminal shutdown error
		// so the select still reaches drain, mirroring dockerevents.Feeder.Run.
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Bytes("stack", debug.Stack()).
					Str("event", "agent_watcher_panic").
					Msg("agent watcher goroutine panicked; converting to terminal shutdown so drain-to-zero/eBPF flush still runs")
				watcherDone <- fmt.Errorf("agent watcher panic: %v", r)
			}
		}()
		watcherDone <- watcher.Run(watcherCtx)
	}()

	// Wire the init executor, CP→clawkerd dialer, and agent-axis subscriptions
	// — see startAgentDialer for the §3.4 degrade contract. agentCleanup is a
	// no-op until agent.Start succeeds, so deferring it is always safe.
	agentCleanup := startAgentDialer(watcherCtx, agentDialerDeps{
		log:         log,
		dockerCli:   dockerCli,
		agentTopic:  agentTopic,
		dockerTopic: dockerTopic,
		agentReg:    agentReg,
		peerLookup:  agentPeerLookup,
		lister:      lister,
		caCertPool:  caCertPool,
	})
	defer func() { agentCleanup() }()

	log.Info().Msg("clawkercp ready")

	// serve until shutdown trigger.
	select {
	case <-signalCtx.Done():
		// The signal value is already queued on sigValueCh; read it non-blocking.
		var sig os.Signal
		select {
		case sig = <-sigValueCh:
		default:
		}
		shutdownLog := log.Info()
		if sig != nil {
			shutdownLog = shutdownLog.Stringer("signal", sig)
		}
		shutdownLog.Msg("shutdown signal received")
		// Subprocess exits past this point are graceful shutdown; suppress
		// crash reporting so it does not race the drain.
		subMgr.BeginShutdown()
		// Cancel + join the watcher for deterministic log ordering (sync.Once
		// makes the teardown safe either way).
		watcherCancel()
		<-watcherDone
		teardownCtx, teardownCancel := context.WithTimeout(context.Background(), cpDrainTimeout)
		if err := drainCallback(teardownCtx); err != nil {
			teardownLog := log.Error().Err(err)
			if sig != nil {
				teardownLog = teardownLog.Stringer("signal", sig)
			}
			teardownLog.Msg("shutdown teardown failed")
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

	// reverse-order graceful shutdown. grpcStack.GracefulStop folds
	// the concurrent admin+agent stop + bounded-timeout + force-Stop fallback
	// (drain step 2 ordering preserved); shutdownCtx bounds both it and the
	// healthz shutdown at defaultShutdownWait.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownWait)
	defer shutdownCancel()
	grpcStack.GracefulStop(shutdownCtx)
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("healthz shutdown error")
	}

	subMgr.Shutdown(defaultShutdownWait)
	log.Info().Msg("clawkercp stopped")
	// drainCallback is idempotent (sync.Once): if a select arm already drained,
	// this returns the captured error without re-running; on arms that didn't
	// drain (watcher already ran it, or a never-served exit) it reports the
	// recorded outcome. Never re-executes the teardown.
	return drainCallback(context.Background())
}

// wireInitExecutor constructs the CP-driven init Executor and applies
// the degrade contract from /controlplane/CLAUDE.md
// ("Resilience contract — CP crashing is a security incident"):
// construction failure logs `agent_init_executor_unavailable` and
// returns nil; CP keeps running. Extracted as its own function so the
// degrade-not-crash invariant is unit-testable — see
// TestWireInitExecutor_NilBus.
func wireInitExecutor(topic *pubsub.Topic[agent.AgentEvent], dockerCli *docker.Client, log *logger.Logger) *agent.Executor {
	exec, err := agent.NewExecutor(topic, dockerCli, log.With("component", "agent.init"))
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
