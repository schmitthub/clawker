package shared

import (
	"context"
	"fmt"

	mobyClient "github.com/moby/moby/client"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/socketbridge"
)

type CommandOpts struct {
	Client         func(context.Context) (*docker.Client, error)
	Config         func() (config.Config, error)
	ProjectManager func() (project.ProjectManager, error)
	HostProxy      func() hostproxy.HostProxyService
	ControlPlane   func() cpboot.Manager
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
	SocketBridge   func() socketbridge.SocketBridgeManager
	Logger         func() (*logger.Logger, error)

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

// NeedsSocketBridge returns true if the project config enables GPG or SSH
// forwarding, which requires a socket bridge daemon.
func NeedsSocketBridge(cfg *config.Project) bool {
	if cfg == nil || cfg.Security.GitCredentials == nil {
		return false
	}
	return cfg.Security.GitCredentials.GPGEnabled() || cfg.Security.GitCredentials.GitSSHEnabled()
}

func BootstrapServicesPreStart(ctx context.Context, container string, cmdOpts CommandOpts) error {
	if cmdOpts.Config == nil {
		return fmt.Errorf("bootstrapping services: config provider is nil")
	}

	cfg, err := cmdOpts.Config()
	if err != nil {
		return fmt.Errorf("bootstrapping services: loading config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("bootstrapping services: config is nil")
	}

	projectCfg := cfg.Project()
	settings := cfg.Settings()

	var log *logger.Logger
	if cmdOpts.Logger != nil {
		log, err = cmdOpts.Logger()
		if err != nil {
			return fmt.Errorf("bootstrapping services: initializing logger: %w", err)
		}
	} else {
		log = logger.Nop()
	}
	// NOTE: do NOT defer log.Close() here. cmdOpts.Logger is a Factory
	// noun (sync.Once-cached singleton) — closing it tears down the
	// underlying lumberjack writer for every other caller in this
	// process and silently kills the audit trail. Lifecycle is owned by
	// Factory; per-command paths must not Close.

	if projectCfg != nil && projectCfg.Security.HostProxyEnabled() {
		if cmdOpts.HostProxy == nil {
			if log != nil {
				log.Debug().Msg("host proxy provider is nil, skipping")
			}
		} else {
			hp := cmdOpts.HostProxy()
			if hp == nil {
				if log != nil {
					log.Debug().Msg("host proxy factory returned nil, skipping")
				}
			} else if err := hp.EnsureRunning(); err != nil {
				return fmt.Errorf("bootstrapping services: ensuring host proxy is running: %w", err)
			} else if log != nil {
				log.Debug().Msg("host proxy started successfully")
			}
		}
	} else if log != nil {
		log.Debug().Msg("host proxy disabled by config")
	}

	// CP is core infrastructure — always bring it up when an agent
	// container is starting. The firewall, future webui, and any other
	// CP-hosted service depend on the CP being live; individual features
	// are configurable, CP itself is not. The container-start path is
	// the single place that bootstraps CP — all other admin commands
	// are pure dials and fail-fast if CP is absent.
	if cmdOpts.ControlPlane == nil {
		return fmt.Errorf("bootstrapping services: no control plane manager provided")
	}
	if err := cmdOpts.ControlPlane().EnsureRunning(ctx); err != nil {
		return fmt.Errorf("bootstrapping services: ensuring control plane is running: %w", err)
	}

	// Firewall is one feature hosted by the CP. Bring the stack up and
	// sync project rules only when firewall.enable (settings.yaml) is
	// true. Per-container FirewallEnable runs post-start because the
	// cgroup only exists after docker start creates the init process.
	if settings != nil && settings.Firewall.FirewallEnabled() {
		if cmdOpts.AdminClient == nil {
			return fmt.Errorf("bootstrapping services: firewall is enabled but no admin client provided")
		}

		adminClient, err := cmdOpts.AdminClient(ctx)
		if err != nil {
			return fmt.Errorf("bootstrapping services: connecting to control plane: %w", err)
		}

		if _, err := adminClient.FirewallInit(ctx, &adminv1.FirewallInitRequest{}); err != nil {
			return fmt.Errorf("bootstrapping services: firewall init: %w", err)
		}

		if cmdOpts.ProjectManager == nil {
			return fmt.Errorf("bootstrapping services: firewall is enabled but no project manager provided")
		}
		pm, err := cmdOpts.ProjectManager()
		if err != nil {
			return fmt.Errorf("bootstrapping services: loading project manager: %w", err)
		}
		proj, err := pm.CurrentProject(ctx)
		if err != nil {
			return fmt.Errorf("bootstrapping services: resolving current project: %w", err)
		}
		if _, err := adminClient.FirewallAddRules(ctx, &adminv1.FirewallAddRulesRequest{
			Rules: adminv1.EgressRulesToProto(proj.EgressRules()),
		}); err != nil {
			return fmt.Errorf("bootstrapping services: adding firewall rules: %w", err)
		}
	}

	// Deliver the every-start pre_run hook to ~/.clawker/pre-run.sh. Always
	// overwrite (user script when set, no-op wrapper when unset) so the
	// on-disk script always reflects current config — value changes and
	// removal are both handled with no staleness. CP runs it (pre-run
	// step) right before the CMD. Not firewall-gated; a copy failure aborts
	// the start.
	if cmdOpts.Client == nil {
		return fmt.Errorf("bootstrapping services: docker client provider is nil")
	}
	client, err := cmdOpts.Client(ctx)
	if err != nil {
		return fmt.Errorf("bootstrapping services: creating docker client: %w", err)
	}
	if client == nil {
		return fmt.Errorf("bootstrapping services: docker client is nil")
	}
	var preRun string
	if projectCfg != nil {
		preRun = projectCfg.Agent.PreRun
	}
	if err := InjectHookScript(ctx, InjectHookOpts{
		ContainerID:     container,
		Script:          preRun,
		Name:            "pre-run",
		Cfg:             cfg,
		CopyToContainer: NewCopyToContainerFn(client),
		Log:             log,
	}); err != nil {
		return fmt.Errorf("bootstrapping services: injecting pre-run script: %w", err)
	}

	return nil
}

func BootstrapServicesPostStart(ctx context.Context, container string, cmdOpts CommandOpts) error {
	if cmdOpts.Config == nil {
		return fmt.Errorf("bootstrapping services: config provider is nil")
	}

	cfg, err := cmdOpts.Config()
	if err != nil {
		return fmt.Errorf("bootstrapping services: loading config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("bootstrapping services: config is nil")
	}

	projectCfg := cfg.Project()
	settings := cfg.Settings()

	var log *logger.Logger
	if cmdOpts.Logger != nil {
		log, err = cmdOpts.Logger()
		if err != nil {
			return fmt.Errorf("bootstrapping services: initializing logger: %w", err)
		}
	} else {
		log = logger.Nop()
	}
	// NOTE: do NOT defer log.Close() here — see PreStart.

	// Enroll this container's cgroup into BPF container_map. Cgroup only
	// exists after docker start creates the container's init process, so
	// this must run post-start. CP + stack + rules came up in pre-start.
	// Drift-guarded per-container enroll (INV-B2-016).
	if settings != nil && settings.Firewall.FirewallEnabled() {
		if cmdOpts.AdminClient == nil {
			return fmt.Errorf("bootstrapping services: firewall is enabled but no admin client provided")
		}

		client, err := cmdOpts.AdminClient(ctx)
		if err != nil {
			return fmt.Errorf("bootstrapping services: connecting to control plane: %w", err)
		}

		if _, err := client.FirewallEnable(ctx, &adminv1.FirewallEnableRequest{
			ContainerId: container,
		}); err != nil {
			return fmt.Errorf("bootstrapping services: enabling firewall for container: %w", err)
		}

		if log != nil {
			log.Debug().Str("container", container).Msg("firewall enabled in container")
		}
	}

	if NeedsSocketBridge(projectCfg) {
		if cmdOpts.SocketBridge == nil {
			if log != nil {
				log.Debug().Msg("socket bridge provider is nil, skipping")
			}
		} else {
			sb := cmdOpts.SocketBridge()
			if sb == nil {
				if log != nil {
					log.Debug().Msg("socket bridge manager is nil, skipping")
				}
			} else {
				gpgEnabled := projectCfg.Security.GitCredentials != nil && projectCfg.Security.GitCredentials.GPGEnabled()
				if err := sb.EnsureBridge(container, gpgEnabled); err != nil {
					if log != nil {
						log.Error().Err(err).Msg("failed to start socket bridge")
					}
					return fmt.Errorf("bootstrapping services: starting socket bridge: %w", err)
				}
			}
		}
	}
	return nil
}

func ContainerStart(ctx context.Context, cmdOpts CommandOpts, startOpts docker.ContainerStartOptions) (mobyClient.ContainerStartResult, error) {
	err := BootstrapServicesPreStart(ctx, startOpts.ContainerID, cmdOpts)
	if err != nil {
		return mobyClient.ContainerStartResult{}, err
	}
	if cmdOpts.Client == nil {
		return mobyClient.ContainerStartResult{}, fmt.Errorf("starting container: docker client provider is nil")
	}
	client, err := cmdOpts.Client(ctx)
	if err != nil {
		return mobyClient.ContainerStartResult{}, fmt.Errorf("starting container: creating docker client: %w", err)
	}
	if client == nil {
		return mobyClient.ContainerStartResult{}, fmt.Errorf("starting container: docker client is nil")
	}

	result, err := client.ContainerStart(ctx, startOpts)
	if err != nil {
		return result, err
	}

	if postErr := BootstrapServicesPostStart(ctx, startOpts.ContainerID, cmdOpts); postErr != nil {
		return result, postErr
	}

	return result, nil
}

// hydraTokenAudienceFromPort returns the canonical `aud` claim value
// for the agent assertion. Pinned to 127.0.0.1 (NOT the CP container's
// docker-network hostname) because Hydra checks `aud` against its own
// `urls.self.issuer` config, regardless of which network path the
// request arrived on.
func hydraTokenAudienceFromPort(port int) string {
	return fmt.Sprintf("https://127.0.0.1:%d/oauth2/token", port)
}
