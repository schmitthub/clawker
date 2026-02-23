---
title: "clawker container"
---

## clawker container

Manage containers

### Synopsis

Manage clawker containers.

This command provides container management operations similar to Docker's
container management commands.

### Examples

```
  # List running containers
  clawker container ls

  # List all containers (including stopped)
  clawker container ls -a

  # Remove a container
  clawker container rm clawker.myapp.dev

  # Stop a running container
  clawker container stop clawker.myapp.dev
```

### Subcommands

* [clawker container attach](clawker_container_attach) - Attach local standard input, output, and error streams to a running container
* [clawker container cp](clawker_container_cp) - Copy files/folders between a container and the local filesystem
* [clawker container create](clawker_container_create) - Create a new container
* [clawker container exec](clawker_container_exec) - Execute a command in a running container
* [clawker container inspect](clawker_container_inspect) - Display detailed information on one or more containers
* [clawker container kill](clawker_container_kill) - Kill one or more running containers
* [clawker container list](clawker_container_list) - List containers
* [clawker container logs](clawker_container_logs) - Fetch the logs of a container
* [clawker container pause](clawker_container_pause) - Pause all processes within one or more containers
* [clawker container remove](clawker_container_remove) - Remove one or more containers
* [clawker container rename](clawker_container_rename) - Rename a container
* [clawker container restart](clawker_container_restart) - Restart one or more containers
* [clawker container run](clawker_container_run) - Create and run a new container
* [clawker container start](clawker_container_start) - Start one or more stopped containers
* [clawker container stats](clawker_container_stats) - Display a live stream of container resource usage statistics
* [clawker container stop](clawker_container_stop) - Stop one or more running containers
* [clawker container top](clawker_container_top) - Display the running processes of a container
* [clawker container unpause](clawker_container_unpause) - Unpause all processes within one or more containers
* [clawker container update](clawker_container_update) - Update configuration of one or more containers
* [clawker container wait](clawker_container_wait) - Block until one or more containers stop, then print their exit codes

### Options

```
  -h, --help   help for container
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
