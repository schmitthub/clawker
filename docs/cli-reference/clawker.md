## clawker

Manage Claude Code in secure Docker containers with clawker

### Synopsis

Clawker (claude + docker) wraps Claude Code in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  clawker init           # Set up user settings (~/.local/clawker/settings.yaml)
  clawker project init   # Initialize project in current directory (clawker.yaml)
  clawker start          # Build and start Claude Code in a container
  clawker stop           # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume

### Subcommands

* [clawker attach](clawker_attach.md) - Attach local standard input, output, and error streams to a running container
* [clawker build](clawker_build.md) - Build an image from a clawker project
* [clawker config](clawker_config.md) - Configuration management commands
* [clawker container](clawker_container.md) - Manage containers
* [clawker cp](clawker_cp.md) - Copy files/folders between a container and the local filesystem
* [clawker create](clawker_create.md) - Create a new container
* [clawker exec](clawker_exec.md) - Execute a command in a running container
* [clawker generate](clawker_generate.md) - Generate Dockerfiles for Claude Code releases
* [clawker image](clawker_image.md) - Manage images
* [clawker init](clawker_init.md) - Initialize clawker user settings
* [clawker kill](clawker_kill.md) - Kill one or more running containers
* [clawker logs](clawker_logs.md) - Fetch the logs of a container
* [clawker monitor](clawker_monitor.md) - Manage local observability stack
* [clawker network](clawker_network.md) - Manage networks
* [clawker pause](clawker_pause.md) - Pause all processes within one or more containers
* [clawker project](clawker_project.md) - Manage clawker projects
* [clawker ps](clawker_ps.md) - List containers
* [clawker ralph](clawker_ralph.md) - Run Claude Code in autonomous loops
* [clawker rename](clawker_rename.md) - Rename a container
* [clawker restart](clawker_restart.md) - Restart one or more containers
* [clawker rm](clawker_rm.md) - Remove one or more containers
* [clawker rmi](clawker_rmi.md) - Remove one or more images
* [clawker run](clawker_run.md) - Create and run a new container
* [clawker start](clawker_start.md) - Start one or more stopped containers
* [clawker stats](clawker_stats.md) - Display a live stream of container resource usage statistics
* [clawker stop](clawker_stop.md) - Stop one or more running containers
* [clawker top](clawker_top.md) - Display the running processes of a container
* [clawker unpause](clawker_unpause.md) - Unpause all processes within one or more containers
* [clawker volume](clawker_volume.md) - Manage volumes
* [clawker wait](clawker_wait.md) - Block until one or more containers stop, then print their exit codes
* [clawker worktree](clawker_worktree.md) - Manage git worktrees for isolated branch development

### Options

```
  -D, --debug   Enable debug logging
  -h, --help    help for clawker
```

