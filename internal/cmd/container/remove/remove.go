package remove

import (
	"context"
	"fmt"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/cobra"
)

// RemoveOptions holds options for the remove command.
type RemoveOptions struct {
	IOStreams      *iostreams.IOStreams
	Client         func(context.Context) (*docker.Client, error)
	ProjectManager func() (project.ProjectManager, error)
	AdminClient    func(context.Context) (adminv1.AdminServiceClient, error)
	SocketBridge   func() socketbridge.SocketBridgeManager
	Logger         func() (*logger.Logger, error)

	Agent   bool
	Force   bool
	Volumes bool

	Containers []string
}

// NewCmdRemove creates the container remove command.
func NewCmdRemove(f *cmdutil.Factory, runF func(context.Context, *RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IOStreams:      f.IOStreams,
		Client:         f.Client,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
		SocketBridge:   f.SocketBridge,
		Logger:         f.Logger,
	}

	cmd := &cobra.Command{
		Use:     "remove [OPTIONS] CONTAINER [CONTAINER...]",
		Aliases: []string{"rm"},
		Short:   "Remove one or more containers",
		Long: `Removes one or more clawker containers.

By default, only stopped containers can be removed. Use --force to remove
running containers.

When --agent is provided, the container names are resolved as clawker.<project>.<agent>
using the project resolved from the current directory.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...`,
		Example: `  # Remove a container using agent name
  clawker container remove --agent dev

  # Remove a stopped container by full name
  clawker container remove clawker.myapp.dev

  # Remove multiple containers
  clawker container rm clawker.myapp.dev clawker.myapp.writer

  # Force remove a running container
  clawker container remove --force --agent dev

  # Remove container and its volumes
  clawker container remove --volumes --agent dev`,
		Args: cmdutil.RequiresMinArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Containers = args
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return removeRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat arguments as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Force remove running containers")
	cmd.Flags().BoolVarP(&opts.Volumes, "volumes", "v", false, "Remove associated volumes")

	return cmd
}

func removeRun(ctx context.Context, opts *RemoveOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	log, err := opts.Logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}

	// Resolve container names
	containers := opts.Containers
	if opts.Agent {
		var projectName string
		if opts.ProjectManager != nil {
			if pm, pmErr := opts.ProjectManager(); pmErr == nil {
				if p, pErr := pm.CurrentProject(ctx); pErr == nil {
					projectName = p.Name()
				}
			}
		}
		resolved, err := docker.ContainerNamesFromAgents(projectName, containers)
		if err != nil {
			return err
		}
		containers = resolved
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	var errs []error
	for _, name := range containers {
		if err := removeContainer(ctx, client, name, opts, log, ios, cs); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.SuccessIcon(), name)
		}
	}

	if len(errs) > 0 {
		return cmdutil.SilentError
	}
	return nil
}

func removeContainer(ctx context.Context, client *docker.Client, name string, opts *RemoveOptions, log *logger.Logger, ios *iostreams.IOStreams, cs *iostreams.ColorScheme) error {
	// Find container by name
	container, err := client.FindContainerByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", name, err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found", name)
	}

	// Disable firewall enforcement for this container (best-effort). Runs
	// before container removal so the CP can resolve the cgroup via Docker.
	// If it fails we still proceed, but surface a user-visible warning
	// because orphaned BPF state leaks until the next firewall restart.
	if opts.AdminClient != nil {
		client, cErr := opts.AdminClient(ctx)
		if cErr != nil {
			log.Warn().Err(cErr).Str("container", container.ID).Msg("failed to reach control plane for firewall disable")
			fmt.Fprintf(ios.ErrOut, "%s firewall disable skipped for %s: could not reach control plane: %v (BPF resources may leak until next firewall restart)\n",
				cs.WarningIcon(), name, cErr)
		} else if _, disableErr := client.FirewallDisable(ctx, &adminv1.FirewallDisableRequest{ContainerId: container.ID}); disableErr != nil {
			log.Warn().Err(disableErr).Str("container", container.ID).Msg("failed to disable firewall")
			fmt.Fprintf(ios.ErrOut, "%s firewall disable failed for %s: %v (BPF resources may leak until next firewall restart)\n",
				cs.WarningIcon(), name, disableErr)
		}
	}

	// Stop socket bridge before removing the container (best-effort)
	if opts.SocketBridge != nil {
		if mgr := opts.SocketBridge(); mgr != nil {
			if err := mgr.StopBridge(container.ID); err != nil {
				log.Warn().Err(err).Str("container", container.ID).Msg("failed to stop socket bridge")
			}
		}
	}

	// Use RemoveContainerWithVolumes if volumes flag is set
	if opts.Volumes {
		if err := client.RemoveContainerWithVolumes(ctx, container.ID, opts.Force); err != nil {
			return err
		}
	} else {
		// Otherwise just remove the container
		if _, err = client.ContainerRemove(ctx, container.ID, opts.Force); err != nil {
			return err
		}
	}

	// Drop the agent row keyed by container_id. Best-effort:
	// if the DB doesn't yet exist (fresh install with no managed
	// container) or the eviction fails, the start path's evict-on-die
	// dockerevents subscription cleans up later. Container removal is
	// the user-facing success of this command, so registry hiccups
	// must not surface as remove failures.
	registryDBPath, pathErr := consts.ControlPlaneDBPath()
	if pathErr != nil {
		log.Debug().Err(pathErr).Msg("agent: skipping evict on remove (db path unresolved)")
		return nil
	}
	reg, openErr := agent.NewSQLiteWriter(registryDBPath, log)
	if openErr != nil {
		log.Debug().Err(openErr).Msg("agent: skipping evict on remove (db open failed)")
		return nil
	}
	defer func() {
		if closer, ok := reg.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()
	if err := reg.EvictByContainerID(container.ID); err != nil {
		// Best-effort: container removal already succeeded above so a
		// registry hiccup must not surface as a remove failure. Log at
		// debug; the dockerevents subscription / startup reap heals
		// the orphan row later.
		log.Debug().Err(err).Str("container_id", container.ID).Msg("agent: evict on remove failed")
	}
	return nil
}
