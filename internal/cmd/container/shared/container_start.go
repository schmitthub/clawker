package shared

import (
	"context"
	"errors"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	mobyClient "github.com/moby/moby/client"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	cpmanager "github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	cpshared "github.com/schmitthub/clawker/internal/cmd/controlplane/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/workspace"
)

type CommandOpts struct {
	// IOStreams is how a control plane that asks for assistance mid-boot
	// reaches the person running the command (see cpshared.AssistSOS).
	// Required: every start path runs under a command that owns a real
	// IOStreams, and a nil here silently downgrades an assistable SOS to
	// a plain error (prompt-suitability is CanPrompt's call, not the
	// wiring's). BootstrapServicesPreStart refuses a nil up front.
	IOStreams *iostreams.IOStreams

	Client        func(context.Context) (*docker.Client, error)
	Config        func() (config.Config, error)
	HostProxy     func() hostproxy.Service
	ControlPlane  func(context.Context) (cpmanager.Manager, error)
	AdminClient   func(context.Context) (adminv1.AdminServiceClient, error)
	SocketBridge  func() socketbridge.SocketBridgeManager
	SocketGrants  func() (db.SocketGrantStore, error)
	Logger        func() (*logger.Logger, error)
	ApproveGrants bool
	Harness       RuntimeHarness

	// AgentName is the user-typed short agent name (e.g. "dev", "test").
	// NOT the AgentFullName "clawker.project.agent" form — the
	// AgentFullName is composed downstream (in MintAgentCert's URI SAN
	// and reconstructed on demand from the registry row's
	// (project, agent_name) columns) from (Project, AgentName) so it
	// has a single home. New-container start paths MUST set this; without it
	// ContainerStart skips the bootstrap-delivery + registry-write and
	// the entrypoint silently skips clawkerd launch. Existing-container
	// start/restart paths leave it empty by design — those containers'
	// registry rows already exist (the CP-side agent dialer picks up
	// where it left off) or were intentionally never registered.
	AgentName string

	// Project is the clawker project slug the agent runs under, paired
	// with AgentName to form the (project, agent) identity the CP keys
	// agentregistry entries by. Empty string signals a global-scope
	// agent (2-segment naming) — same convention as
	// docker.ContainerName. Must be set whenever AgentName is set on a
	// new-container start path so MintAgentCert composes the right
	// AgentFullName URI SAN.
	Project string
}

// RuntimeHarness contains the harness values that a command loaded for one
// container. Start plumbing consumes these values and does not read the
// harness configuration again.
type RuntimeHarness struct {
	Name              string
	Provenance        bundle.Provenance
	Sockets           []config.HarnessSocket
	Egress            []config.EgressRule
	HasContainerLabel bool
}

// NeedsSocketBridge returns true if the project's security config enables GPG
// or SSH forwarding, which requires a socket bridge daemon.
func NeedsSocketBridge(security config.SecurityConfig) bool {
	if security.GitCredentials == nil {
		return false
	}
	return security.GitCredentials.GPGEnabled() || security.GitCredentials.GitSSHEnabled()
}

// ensureHostProxyRunning starts the host proxy when the project enables it.
// A nil provider or a nil proxy instance is a no-op (debug-logged); only a
// failure from EnsureRunning aborts the start. log may be nil.
func ensureHostProxyRunning(
	security config.SecurityConfig,
	hostProxyFn func() hostproxy.Service,
	log *logger.Logger,
) error {
	if !security.HostProxyEnabled() {
		if log != nil {
			log.Debug().Msg("host proxy disabled by config")
		}
		return nil
	}

	if hostProxyFn == nil {
		if log != nil {
			log.Debug().Msg("host proxy provider is nil, skipping")
		}
		return nil
	}

	hp := hostProxyFn()
	if hp == nil {
		if log != nil {
			log.Debug().Msg("host proxy factory returned nil, skipping")
		}
		return nil
	}

	if err := hp.EnsureRunning(); err != nil {
		return fmt.Errorf("bootstrapping services: ensuring host proxy is running: %w", err)
	}
	if log != nil {
		log.Debug().Msg("host proxy started successfully")
	}
	return nil
}

func BootstrapServicesPreStart(
	ctx context.Context,
	container string,
	cmdOpts CommandOpts,
) ([]socketbridge.BridgedSocket, error) {
	if cmdOpts.Config == nil {
		return nil, errors.New("bootstrapping services: config provider is nil")
	}
	// A nil IOStreams is always a wiring bug, never a headless caller —
	// non-interactive runs carry a non-TTY IOStreams and are filtered by
	// CanPrompt inside AssistSOS. Failing loud here keeps a forgotten
	// field from silently declining control plane assistance requests.
	if cmdOpts.IOStreams == nil {
		return nil, errors.New("bootstrapping services: no IOStreams provided")
	}

	cfg, configErr := bootstrapConfig(cmdOpts)
	if configErr != nil {
		return nil, configErr
	}
	log, loggerErr := bootstrapLogger(cmdOpts)
	if loggerErr != nil {
		return nil, loggerErr
	}
	// NOTE: do NOT defer log.Close() here. cmdOpts.Logger is a Factory
	// noun (sync.Once-cached singleton) — closing it tears down the
	// underlying lumberjack writer for every other caller in this
	// process and silently kills the audit trail. Lifecycle is owned by
	// Factory; per-command paths must not Close.

	return bootstrapPreparedServices(ctx, container, cfg, cmdOpts, log)
}

func bootstrapPreparedServices(
	ctx context.Context,
	container string,
	cfg config.Config,
	cmdOpts CommandOpts,
	log *logger.Logger,
) ([]socketbridge.BridgedSocket, error) {
	// CP is core infrastructure. Every container start must start it.
	if controlPlaneErr := ensureControlPlane(ctx, cmdOpts); controlPlaneErr != nil {
		return nil, controlPlaneErr
	}

	client, clientErr := bootstrapDockerClient(ctx, cmdOpts)
	if clientErr != nil {
		return nil, clientErr
	}

	// Containers created against ID-mapped workspace views (rootless
	// daemons) need those views mounted BEFORE Docker resolves the bind
	// sources at start — they die at reboot, and starting over the bare
	// mount-point directory hands the container an empty workspace.
	if viewErr := ensureIDMappedViewsAtStart(ctx, client, container, cmdOpts.IOStreams, log); viewErr != nil {
		return nil, fmt.Errorf("bootstrapping services: %w", viewErr)
	}

	harness := cmdOpts.Harness
	if harness.Name == "" {
		return nil, errors.New("bootstrapping services: runtime harness is not loaded")
	}

	bridgedSockets, authorizationErr := authorizeSocketBridges(
		container,
		harness,
		cmdOpts,
		log,
	)
	if authorizationErr != nil {
		return nil, fmt.Errorf("bootstrapping services: %w", authorizationErr)
	}
	if injectErr := injectSocketsWaitHook(ctx, container, bridgedSockets, cfg, client, log); injectErr != nil {
		return nil, fmt.Errorf("bootstrapping services: injecting sockets-wait script: %w", injectErr)
	}

	if serviceErr := bootstrapPreStartServices(ctx, cfg, harness, cmdOpts, log); serviceErr != nil {
		return nil, serviceErr
	}

	// Deliver the every-start pre_run hook to ~/.clawker/pre-run.sh. Always
	// overwrite (user script when set, no-op wrapper when unset) so the
	// on-disk script always reflects current config — value changes and
	// removal are both handled with no staleness. CP runs it (pre-run
	// step) right before the CMD. Not firewall-gated; a copy failure aborts
	// the start.
	if injectErr := injectPreRunHook(ctx, container, harness.Name, cfg, client, log); injectErr != nil {
		return nil, fmt.Errorf("bootstrapping services: injecting pre-run script: %w", injectErr)
	}

	return bridgedSockets, nil
}

func injectSocketsWaitHook(
	ctx context.Context,
	container string,
	bridgedSockets []socketbridge.BridgedSocket,
	cfg config.Config,
	client *docker.Client,
	log *logger.Logger,
) error {
	return InjectHookScript(ctx, InjectHookOpts{
		ContainerID:     container,
		Script:          socketsWaitScript(bridgedSockets, socketWaitTimeoutSeconds),
		Shell:           "",
		Name:            consts.HookSocketsWait,
		Cfg:             cfg,
		CopyToContainer: NewCopyToContainerFn(client),
		Log:             log,
	})
}

func injectPreRunHook(
	ctx context.Context,
	container string,
	harnessName string,
	cfg config.Config,
	client *docker.Client,
	log *logger.Logger,
) error {
	return InjectHookScript(ctx, InjectHookOpts{
		ContainerID:     container,
		Script:          cfg.PreRunFor(harnessName),
		Shell:           "",
		Name:            consts.HookPreRun,
		Cfg:             cfg,
		CopyToContainer: NewCopyToContainerFn(client),
		Log:             log,
	})
}

func bootstrapPreStartServices(
	ctx context.Context,
	cfg config.Config,
	harness RuntimeHarness,
	cmdOpts CommandOpts,
	log *logger.Logger,
) error {
	// Firewall is one feature hosted by the CP. Bring the stack up and
	// sync project rules only when firewall.enable (settings.yaml) is
	// true. Per-container FirewallEnable runs post-start because the
	// cgroup only exists after docker start creates the init process.
	if cfg.FirewallEnabled() {
		if fwErr := bringUpFirewall(ctx, cmdOpts, cfg, harness.Egress); fwErr != nil {
			return fwErr
		}
	}
	return ensureHostProxyRunning(cfg.SecurityConfig(), cmdOpts.HostProxy, log)
}

//nolint:ireturn // Commands use the project config seam.
func bootstrapConfig(
	cmdOpts CommandOpts,
) (config.Config, error) {
	if cmdOpts.Config == nil {
		return nil, errors.New("bootstrapping services: config provider is nil")
	}
	cfg, err := cmdOpts.Config()
	if err != nil {
		return nil, fmt.Errorf("bootstrapping services: loading config: %w", err)
	}
	if cfg == nil {
		return nil, errors.New("bootstrapping services: config is nil")
	}
	return cfg, nil
}

func bootstrapLogger(cmdOpts CommandOpts) (*logger.Logger, error) {
	if cmdOpts.Logger == nil {
		return logger.Nop(), nil
	}
	log, err := cmdOpts.Logger()
	if err != nil {
		return nil, fmt.Errorf("bootstrapping services: initializing logger: %w", err)
	}
	return log, nil
}

func ensureControlPlane(ctx context.Context, cmdOpts CommandOpts) error {
	if cmdOpts.ControlPlane == nil {
		return errors.New("bootstrapping services: no control plane manager provided")
	}
	mgr, cpErr := cmdOpts.ControlPlane(ctx)
	if cpErr != nil {
		return fmt.Errorf("bootstrapping services: %w", cpErr)
	}
	startErr := mgr.Start(ctx)
	if sos, ok := errors.AsType[*cpmanager.CPSOSError](startErr); ok {
		if assistErr := cpshared.AssistSOS(ctx, sos, cmdOpts.IOStreams); assistErr != nil {
			return fmt.Errorf("bootstrapping services: ensuring control plane is running: %w", assistErr)
		}
		startErr = mgr.Start(ctx)
	}
	if startErr != nil {
		return fmt.Errorf("bootstrapping services: ensuring control plane is running: %w", startErr)
	}
	return nil
}

func bootstrapDockerClient(ctx context.Context, cmdOpts CommandOpts) (*docker.Client, error) {
	if cmdOpts.Client == nil {
		return nil, errors.New("bootstrapping services: docker client provider is nil")
	}
	client, err := cmdOpts.Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrapping services: creating docker client: %w", err)
	}
	if client == nil {
		return nil, errors.New("bootstrapping services: docker client is nil")
	}
	return client, nil
}

// bringUpFirewall performs the pre-start half of firewall bootstrap: dial the
// CP, bring the Envoy+CoreDNS stack up, and push the container's composed
// egress rules (harness floor + project rules) into the rules store. The
// per-container cgroup enroll is deliberately NOT here — the cgroup only
// exists once docker start has created the init process, so that half lives in
// BootstrapServicesPostStart.
func bringUpFirewall(
	ctx context.Context,
	cmdOpts CommandOpts,
	cfg config.Config,
	harnessEgress []config.EgressRule,
) error {
	if cmdOpts.AdminClient == nil {
		return errors.New("bootstrapping services: firewall is enabled but no admin client provided")
	}

	adminClient, dialErr := cmdOpts.AdminClient(ctx)
	if dialErr != nil {
		return fmt.Errorf("bootstrapping services: connecting to control plane: %w", dialErr)
	}

	if _, initErr := adminClient.FirewallInit(ctx, &adminv1.FirewallInitRequest{}); initErr != nil {
		return fmt.Errorf("bootstrapping services: firewall init: %w", initErr)
	}

	egressRules := bundler.ComposeEgressRules(cfg, harnessEgress)
	if _, addErr := adminClient.FirewallAddRules(ctx, &adminv1.FirewallAddRulesRequest{
		Rules: adminv1.EgressRulesToProto(egressRules),
	}); addErr != nil {
		return fmt.Errorf("bootstrapping services: adding firewall rules: %w", addErr)
	}
	return nil
}

// containerHarnessName reads the container's harness label — the identity
// stamped at create from the image — so start composes against what the
// container actually is, not whatever build.harness says today.
//
// Fallback to the configured default happens in exactly two benign cases,
// each logged with its reason: the container carries no harness label
// (created before the label existed) or it is not found / not
// clawker-managed. Any other inspect failure surfaces — a daemon error must
// not silently resolve to a harness the container may not contain.
func containerHarnessName(
	ctx context.Context,
	client *docker.Client,
	cfg config.Config,
	container string,
	log *logger.Logger,
) (string, bool, error) {
	if log == nil {
		log = logger.Nop()
	}
	reason := "container carries no harness label"
	inspect, err := client.ContainerInspect(ctx, container, docker.ContainerInspectOptions{Size: false})
	switch {
	case err == nil:
		if inspect.Container.Config != nil {
			if name := inspect.Container.Config.Labels[consts.LabelHarness]; name != "" {
				return name, true, nil
			}
		}
	case docker.IsNotFound(err):
		reason = "container not found or not clawker-managed"
	default:
		return "", false, fmt.Errorf("resolving harness identity: inspect container %s: %w", container, err)
	}
	name, resolveErr := bundler.ResolveHarnessName(cfg, "")
	if resolveErr != nil {
		return "", false, fmt.Errorf(
			"container %s carries no harness label and default resolution failed: %w", container, resolveErr)
	}
	log.Warn().
		Str("container", container).
		Str("harness", name).
		Str("reason", reason).
		Msg("falling back to the configured default harness")
	return name, false, nil
}

// LoadContainerHarness reads the container identity and loads its harness once
// with the canonical harness reader. Command run functions call this helper and
// pass selected values in a RuntimeHarness literal.
func LoadContainerHarness(
	ctx context.Context,
	client *docker.Client,
	cfg config.Config,
	container string,
	log *logger.Logger,
) (*bundler.Bundle, bool, error) {
	harnessName, hasHarnessLabel, err := containerHarnessName(ctx, client, cfg, container, log)
	if err != nil {
		return nil, false, fmt.Errorf("resolve container harness: %w", err)
	}
	harness, err := bundler.LoadHarness(cfg, harnessName)
	if err != nil {
		return nil, false, fmt.Errorf(
			"container's harness %q no longer loads (%w); it may name a bundle that was removed or"+
				" is not installed — reinstall it with `clawker bundle install` or rebuild the image with"+
				" `clawker build`",
			harnessName,
			err,
		)
	}
	return harness, hasHarnessLabel, nil
}

func BootstrapServicesPostStart(
	ctx context.Context,
	container string,
	bridgedSockets []socketbridge.BridgedSocket,
	cmdOpts CommandOpts,
) error {
	cfg, configErr := bootstrapConfig(cmdOpts)
	if configErr != nil {
		return configErr
	}
	log, loggerErr := bootstrapLogger(cmdOpts)
	if loggerErr != nil {
		return loggerErr
	}
	security := cfg.SecurityConfig()
	// NOTE: do NOT defer log.Close() here — see PreStart.

	// Enroll this container's cgroup into BPF container_map. Cgroup only
	// exists after docker start creates the container's init process, so
	// this must run post-start. CP + stack + rules came up in pre-start.
	// Drift-guarded per-container enroll (INV-B2-016).
	if cfg.FirewallEnabled() {
		if firewallErr := enableContainerFirewall(ctx, container, cmdOpts, log); firewallErr != nil {
			return firewallErr
		}
	}

	precheckForwarders(ctx, security, cmdOpts, log)

	if NeedsSocketBridge(security) || len(bridgedSockets) > 0 {
		if bridgeErr := startSocketBridge(container, security, bridgedSockets, cmdOpts, log); bridgeErr != nil {
			return bridgeErr
		}
	}
	return nil
}

func enableContainerFirewall(
	ctx context.Context,
	container string,
	cmdOpts CommandOpts,
	log *logger.Logger,
) error {
	if cmdOpts.AdminClient == nil {
		return errors.New("bootstrapping services: firewall is enabled but no admin client provided")
	}
	client, dialErr := cmdOpts.AdminClient(ctx)
	if dialErr != nil {
		return fmt.Errorf("bootstrapping services: connecting to control plane: %w", dialErr)
	}
	if _, enableErr := client.FirewallEnable(ctx, &adminv1.FirewallEnableRequest{
		ContainerId: container,
	}); enableErr != nil {
		return fmt.Errorf("bootstrapping services: enabling firewall for container: %w", enableErr)
	}
	log.Debug().Str("container", container).Msg("firewall enabled in container")
	return nil
}

// startSocketBridge brings up the per-container SSH/GPG agent socket bridge.
// An unwired provider or a nil manager means forwarding is simply unavailable
// in this process — it is logged and skipped, not an error. Only a bridge that
// was asked for and failed to start aborts the start sequence.
func startSocketBridge(
	container string,
	security config.SecurityConfig,
	bridgedSockets []socketbridge.BridgedSocket,
	cmdOpts CommandOpts,
	log *logger.Logger,
) error {
	if cmdOpts.SocketBridge == nil {
		if log != nil {
			log.Debug().Msg("socket bridge provider is nil, skipping")
		}
		return nil
	}
	sb := cmdOpts.SocketBridge()
	if sb == nil {
		if log != nil {
			log.Debug().Msg("socket bridge manager is nil, skipping")
		}
		return nil
	}
	gpgEnabled := security.GitCredentials != nil && security.GitCredentials.GPGEnabled()
	if err := sb.EnsureBridge(socketbridge.EnsureBridgeOpts{
		ContainerID: container,
		GPGEnabled:  gpgEnabled,
		Sockets:     bridgedSockets,
	}); err != nil {
		if log != nil {
			log.Error().Err(err).Msg("failed to start socket bridge")
		}
		return fmt.Errorf("bootstrapping services: starting socket bridge: %w", err)
	}
	return nil
}

// precheckForwarders checks every configured credential-forwarding lane
// against the host, before any forwarding service starts. Each check mirrors
// the exact deterministic lookup the serving component performs (socket
// bridge lanes, host proxy git credential, gitconfig mount), and each
// configured lane the host cannot serve gets one stderr warning — so a later
// forwarding failure has a visible cause. Prechecks never change behavior:
// they warn, nothing else.
func precheckForwarders(
	ctx context.Context,
	security config.SecurityConfig,
	cmdOpts CommandOpts,
	log *logger.Logger,
) {
	gc := security.GitCredentials
	if gc == nil {
		return
	}
	precheckBridgeLanes(ctx, gc, cmdOpts, log)
	if gc.CopyGitConfigEnabled() && !workspace.GitConfigExists() {
		warnForwarderLane(cmdOpts, log,
			"Git config copy is configured but no ~/.gitconfig was found on this host")
	}
	precheckHTTPSLane(ctx, security, cmdOpts, log)
}

// precheckBridgeLanes checks the GPG and SSH lanes through the socket
// bridge manager's own host precheck. An unwired provider means nothing
// serves those lanes in this process — nothing to check.
func precheckBridgeLanes(
	ctx context.Context,
	gc *config.GitCredentialsConfig,
	cmdOpts CommandOpts,
	log *logger.Logger,
) {
	lanes := socketbridge.PrecheckOptions{GPG: gc.GPGEnabled(), SSH: gc.GitSSHEnabled()}
	if !lanes.GPG && !lanes.SSH {
		return
	}
	if cmdOpts.SocketBridge == nil {
		return
	}
	sb := cmdOpts.SocketBridge()
	if sb == nil {
		return
	}
	err := sb.Precheck(ctx, lanes)
	if err == nil {
		return
	}
	if lanes.GPG && errors.Is(err, socketbridge.ErrGPGUnavailable) {
		warnForwarderLane(cmdOpts, log,
			"GPG forwarding is configured but no usable GPG keys or gpg-agent were found on this host")
	}
	if lanes.SSH && errors.Is(err, socketbridge.ErrSSHAgentUnavailable) {
		warnForwarderLane(cmdOpts, log,
			"SSH forwarding is configured but no SSH agent was found on this host")
	}
}

// precheckHTTPSLane checks the HTTPS credential lane through the host
// proxy's own precheck. An unwired provider means nothing serves the lane
// in this process — nothing to check.
func precheckHTTPSLane(
	ctx context.Context,
	security config.SecurityConfig,
	cmdOpts CommandOpts,
	log *logger.Logger,
) {
	if !security.GitCredentials.GitHTTPSEnabled(security.HostProxyEnabled()) {
		return
	}
	if cmdOpts.HostProxy == nil {
		return
	}
	hp := cmdOpts.HostProxy()
	if hp == nil {
		return
	}
	if err := hp.PrecheckGitCredential(ctx); err != nil {
		warnForwarderLane(cmdOpts, log,
			"HTTPS credential forwarding is configured but the host cannot serve it: "+err.Error())
	}
}

// warnForwarderLane emits a lane warning to both the user (stderr) and the log.
func warnForwarderLane(cmdOpts CommandOpts, log *logger.Logger, msg string) {
	if log != nil {
		log.Warn().Msg(msg)
	}
	if ios := cmdOpts.IOStreams; ios != nil {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().WarningIcon(), msg)
	}
}

// ReapedNotice is appended to a start error when ReapFailedStart removed the
// never-started auto-remove container. Callers and tests match on this const
// rather than the raw wording.
const ReapedNotice = "container was set to auto-remove and never started; removed it — safe to re-run"

// ReapFailedStart removes a container whose start sequence failed, but only
// when the container is destined for AutoRemove (--rm) and its inspect state
// proves it is not running. A nil State is treated as unknown and left
// untouched — a force-remove demands proof the container isn't running.
// Docker honors AutoRemove solely on exit-after-start, so a container whose
// start never succeeded would otherwise squat its name forever in the
// created state. Non-AutoRemove containers are left untouched — standard
// docker semantics. A NotFound from inspect or remove is benign: the daemon
// already removed the container (e.g. AutoRemove after a kill), so the goal
// state holds. Always returns a non-nil error derived from startErr so
// callers can return it directly. Cleanup runs on a background context so
// caller cancellation (Ctrl+C) cannot abort it.
func ReapFailedStart(client *docker.Client, containerID string, startErr error) error {
	ctx := context.Background()
	res, inspErr := client.ContainerInspect(ctx, containerID, mobyClient.ContainerInspectOptions{})
	if inspErr != nil {
		if reapTargetGone(inspErr) {
			// Container already gone — nothing left to reap.
			return startErr
		}
		return fmt.Errorf("%w; additionally, inspecting container for cleanup failed: %w", startErr, inspErr)
	}
	c := res.Container
	if c.HostConfig == nil || !c.HostConfig.AutoRemove || c.State == nil || c.State.Running {
		return startErr
	}
	if _, rmErr := client.ContainerRemove(ctx, containerID, true); rmErr != nil && !reapTargetGone(rmErr) {
		return fmt.Errorf("%w; additionally, the auto-remove container could not be removed: %w", startErr, rmErr)
	}
	return fmt.Errorf("%w (%s)", startErr, ReapedNotice)
}

// reapTargetGone reports whether a reap inspect/remove error means the
// container no longer exists — the benign race where the daemon removed it
// first (e.g. AutoRemove after a kill). It shows up two ways: the whail jail
// collapses a NotFound during its managed check to ErrNotManaged, and a
// vanish between the managed check and the API call surfaces the daemon's
// own NotFound.
func reapTargetGone(err error) bool {
	return cerrdefs.IsNotFound(err) || errors.Is(err, docker.ErrNotManaged)
}

// ContainerStart runs the three start phases: pre-start bootstrap, the Docker
// start call, post-start bootstrap. Pre-start and Docker-start failures route
// through ReapFailedStart; a post-start failure does not (the container is
// running by then). The result is the SDK's verbatim — nil means the Docker
// start call was never reached (this function NEVER fabricates an SDK result
// value; moby reserves the right to add fields to ContainerStartResult, and
// an invented zero value would silently misrepresent them).
func ContainerStart(
	ctx context.Context,
	cmdOpts CommandOpts,
	startOpts docker.ContainerStartOptions,
) (*mobyClient.ContainerStartResult, error) {
	if cmdOpts.Client == nil {
		return nil, fmt.Errorf("starting container: docker client provider is nil")
	}
	client, err := cmdOpts.Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting container: creating docker client: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("starting container: docker client is nil")
	}

	bridgedSockets, err := BootstrapServicesPreStart(ctx, startOpts.ContainerID, cmdOpts)
	if err != nil {
		//nolint:contextcheck // reap is cleanup — it must run on Background even when ctx is dead
		return nil, ReapFailedStart(
			client,
			startOpts.ContainerID,
			fmt.Errorf("pre-start bootstrapping failed: %w", err),
		)
	}
	result, err := client.ContainerStart(ctx, startOpts)
	if err != nil {
		return &result, ReapFailedStart(client, startOpts.ContainerID, fmt.Errorf("starting container: %w", err))
	}

	if postErr := BootstrapServicesPostStart(ctx, startOpts.ContainerID, bridgedSockets, cmdOpts); postErr != nil {
		return &result, postErr
	}

	return &result, nil
}

// hydraTokenAudienceFromPort returns the canonical `aud` claim value
// for the agent assertion. Pinned to 127.0.0.1 (NOT the CP container's
// docker-network hostname) because Hydra checks `aud` against its own
// `urls.self.issuer` config, regardless of which network path the
// request arrived on.
func hydraTokenAudienceFromPort(port int) string {
	return fmt.Sprintf("https://"+consts.Localhost+":%d/oauth2/token", port)
}
