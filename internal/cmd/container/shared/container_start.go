package shared

import (
	"context"
	"fmt"
	"time"

	mobyClient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
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
	Firewall       func(context.Context) (firewall.FirewallManager, error)
	SocketBridge   func() socketbridge.SocketBridgeManager
	Logger         func() (*logger.Logger, error)
}

// NeedsSocketBridge returns true if the project config enables GPG or SSH
// forwarding, which requires a socket bridge daemon.
func NeedsSocketBridge(cfg *config.Project) bool {
	if cfg == nil || cfg.Security.GitCredentials == nil {
		return false
	}
	return cfg.Security.GitCredentials.GPGEnabled() || cfg.Security.GitCredentials.GitSSHEnabled()
}

func BootstrapServices(ctx context.Context, container string, cmdOpts CommandOpts) error {
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

	// Start socket bridge for GPG/SSH forwarding (fire-and-forget for detached)
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

	// Ensure firewall is running (if enabled)
	if settings != nil && settings.Firewall.FirewallEnabled() {
		if cmdOpts.Firewall == nil {
			if log != nil {
				log.Debug().Msg("firewall manager provider is nil, skipping")
			}
		} else {
			fwMgr, fwMgrErr := cmdOpts.Firewall(ctx)
			if fwMgrErr != nil {
				if log != nil {
					log.Error().Err(fwMgrErr).Msg("initialize firewall manager error")
				}
				return fmt.Errorf("bootstrapping services: initializing firewall manager: %w", fwMgrErr)
			}
			if fwMgr == nil {
				if log != nil {
					log.Debug().Msg("firewall manager is nil, skipping")
				}
			} else {
				// Sync project rules — writes configs, restarts containers only if running.
				projectRules := firewall.ProjectRules(cfg)
				if err := fwMgr.AddRules(ctx, projectRules); err != nil {
					return fmt.Errorf("bootstrapping services: adding firewall rules: %w", err)
				}

				fwDaemonErr := firewall.EnsureDaemon(cfg, log)
				if fwDaemonErr != nil {
					if log != nil {
						log.Error().Err(fwDaemonErr).Msg("ensure firewall daemon error")
					}
					return fmt.Errorf("bootstrapping services: ensuring firewall daemon: %w", fwDaemonErr)
				}
				waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
				defer waitCancel()
				if err := fwMgr.WaitForHealthy(waitCtx); err != nil {
					return fmt.Errorf("bootstrapping services: waiting for firewall health: %w", err)
				}
			}
		}
	}

	// Ensure host proxy is running (if enabled)
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

	return nil
}

func ContainerStart(ctx context.Context, cmdOpts CommandOpts, startOpts docker.ContainerStartOptions) (mobyClient.ContainerStartResult, error) {
	err := BootstrapServices(ctx, startOpts.ContainerID, cmdOpts)
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
	return client.ContainerStart(ctx, startOpts)
}
