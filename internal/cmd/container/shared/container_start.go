package shared

import (
	"context"
	"fmt"

	mobyClient "github.com/moby/moby/client"
	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
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

	// AgentName is the canonical "clawker.<project>.<agent>" name.
	// New-container start paths MUST set it; without it ContainerStart
	// skips the announce + bootstrap-delivery and the entrypoint
	// silently skips clawkerd launch. Existing-container start/restart
	// paths leave it empty by design — those containers' slots either
	// already exist (and clawkerd is reconnecting, future B5 work) or
	// were intentionally never registered.
	AgentName string
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
	if log != nil {
		defer log.Close()
	}

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

		client, err := cmdOpts.AdminClient(ctx)
		if err != nil {
			return fmt.Errorf("bootstrapping services: connecting to control plane: %w", err)
		}

		if _, err := client.FirewallInit(ctx, &adminv1.FirewallInitRequest{}); err != nil {
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
		if _, err := client.FirewallAddRules(ctx, &adminv1.FirewallAddRulesRequest{
			Rules: fwcp.ConfigRulesToProto(proj.EgressRules()),
		}); err != nil {
			return fmt.Errorf("bootstrapping services: adding firewall rules: %w", err)
		}
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
	if log != nil {
		defer log.Close()
	}

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

	// Announce the agent slot + write the bootstrap material into the
	// container BEFORE docker start. Hard-fail policy: any error
	// returns from ContainerStart before client.ContainerStart fires —
	// the container is created but not started, and the caller's
	// existing cleanup handles teardown. Empty AgentName skips the
	// bootstrap (existing-container start/restart paths).
	if cmdOpts.AgentName != "" {
		if err := prepareAgentBootstrap(ctx, cmdOpts, startOpts.ContainerID, NewCopyToContainerFn(client)); err != nil {
			return mobyClient.ContainerStartResult{}, fmt.Errorf("agent bootstrap: %w", err)
		}
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

// prepareAgentBootstrap mints fresh PKCE + per-agent mTLS material,
// announces the slot to the CP via AdminService.AnnounceAgent, then
// tars the bootstrap directory into the container at consts.BootstrapDir
// (parent dir 0700, files 0400). Caller invokes this BEFORE
// client.ContainerStart so:
//
//   - The slot is reserved in the CP before clawkerd boots and dials
//     Connect (otherwise clawkerd's first Recv hits an unknown-slot
//     rejection).
//   - The bootstrap files are present in the writable layer when the
//     container's entrypoint reads them (Docker's CopyToContainer can't
//     pre-populate a tmpfs, so the writable layer is the only viable
//     pre-start landing zone).
//
// Hard-fails the whole start path on any error — partial bootstrap
// states are unreachable. Caller's deferred cleanup (or the user's
// next `clawker run`) decides whether to retry.
//
// copyFn is injected (rather than derived from a *docker.Client inside
// the helper) so unit tests can capture the tar payload landing in
// the container without standing up a Docker daemon.
func prepareAgentBootstrap(ctx context.Context, cmdOpts CommandOpts, containerID string, copyFn CopyToContainerFn) error {
	if cmdOpts.Config == nil {
		return fmt.Errorf("config provider is nil")
	}
	cfg, err := cmdOpts.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if cmdOpts.AdminClient == nil {
		return fmt.Errorf("admin client provider is nil")
	}

	caCertPath, err := consts.AuthCACertPath()
	if err != nil {
		return fmt.Errorf("ca cert path: %w", err)
	}
	caKeyPath, err := consts.AuthCAKeyPath()
	if err != nil {
		return fmt.Errorf("ca key path: %w", err)
	}
	signingKey, err := auth.LoadSigningKey()
	if err != nil {
		return fmt.Errorf("load signing key: %w", err)
	}

	// The assertion's audience claim must match Hydra's
	// `urls.self.issuer` config (`https://127.0.0.1:<port>/`) — NOT
	// the URL clawkerd POSTs to. Hydra checks `aud` against its own
	// self-identity, regardless of which network path the request
	// arrived on. The CLI side gets away with one URL because the CLI
	// runs on the host and 127.0.0.1:<port> IS Hydra. Inside a
	// container clawkerd POSTs to `clawker-controlplane:<port>` (Docker
	// DNS — see EnvClawkerdHydraURL set by buildCreateTimeEnv) but
	// signs the assertion with the 127.0.0.1 audience so Hydra
	// accepts it.
	hydraTokenAudience := fmt.Sprintf("https://127.0.0.1:%d/oauth2/token",
		cfg.Settings().ControlPlane.HydraPublicPort)

	bootstrap, err := GenerateAgentBootstrap(caCertPath, caKeyPath, cmdOpts.AgentName, hydraTokenAudience, signingKey)
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}

	admin, err := cmdOpts.AdminClient(ctx)
	if err != nil {
		return fmt.Errorf("dial control plane: %w", err)
	}
	if err := AnnounceAgent(ctx, admin, bootstrap, cmdOpts.AgentName, containerID); err != nil {
		return fmt.Errorf("announce: %w", err)
	}
	if err := WriteAgentBootstrapToContainer(ctx, containerID, copyFn, bootstrap); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
