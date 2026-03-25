---
title: "clawker"
---

## clawker

Manage Claude Code in secure Docker containers with clawker

### Synopsis

Clawker (claude + docker) wraps Claude Code in safe, reproducible, monitored, isolated Docker containers.

Quick start:
  clawker init           # Initialize project in current directory (.clawker.yaml)
  clawker build          # Build the container image
  clawker run            # Start Claude Code in a container
  clawker stop           # Stop the container

Workspace modes:
  --mode=bind          Live sync with host (default)
  --mode=snapshot      Isolated copy in Docker volume

### Subcommands

* [clawker attach](clawker_attach) - Attach local standard input, output, and error streams to a running container
* [clawker build](clawker_build) - Build an image from a clawker project
* [clawker container](clawker_container) - Manage containers
* [clawker cp](clawker_cp) - Copy files/folders between a container and the local filesystem
* [clawker create](clawker_create) - Create a new container
* [clawker exec](clawker_exec) - Execute a command in a running container
* [clawker firewall](clawker_firewall) - Manage the egress firewall
* [clawker generate](clawker_generate) - Generate Dockerfiles for Claude Code releases
* [clawker image](clawker_image) - Manage images
* [clawker init](clawker_init) - Initialize a new clawker project (alias for 'project init')
* [clawker kill](clawker_kill) - Kill one or more running containers
* [clawker logs](clawker_logs) - Fetch the logs of a container
* [clawker loop](clawker_loop) - Run Claude Code in autonomous loops
* [clawker monitor](clawker_monitor) - Manage local observability stack
* [clawker network](clawker_network) - Manage networks
* [clawker pause](clawker_pause) - Pause all processes within one or more containers
* [clawker project](clawker_project) - Manage clawker projects
* [clawker ps](clawker_ps) - List containers
* [clawker rename](clawker_rename) - Rename a container
* [clawker restart](clawker_restart) - Restart one or more containers
* [clawker rm](clawker_rm) - Remove one or more containers
* [clawker rmi](clawker_rmi) - Remove one or more images
* [clawker run](clawker_run) - Create and run a new container
* [clawker settings](clawker_settings) - Manage clawker user settings
* [clawker start](clawker_start) - Start one or more stopped containers
* [clawker stats](clawker_stats) - Display a live stream of container resource usage statistics
* [clawker stop](clawker_stop) - Stop one or more running containers
* [clawker top](clawker_top) - Display the running processes of a container
* [clawker unpause](clawker_unpause) - Unpause all processes within one or more containers
* [clawker volume](clawker_volume) - Manage volumes
* [clawker wait](clawker_wait) - Block until one or more containers stop, then print their exit codes
* [clawker worktree](clawker_worktree) - Manage git worktrees for isolated branch development

### Options

```
  -D, --debug   Enable debug logging
  -h, --help    help for clawker
```

