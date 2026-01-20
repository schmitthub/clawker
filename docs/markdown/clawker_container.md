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
  clawker container rm clawker.myapp.ralph

  # Stop a running container
  clawker container stop clawker.myapp.ralph
```

### Subcommands

* [clawker container attach](clawker_container_attach.md) - Attach local standard input, output, and error streams to a running container
* [clawker container cp](clawker_container_cp.md) - Copy files/folders between a container and the local filesystem
* [clawker container create](clawker_container_create.md) - Create a new container
* [clawker container exec](clawker_container_exec.md) - Execute a command in a running container
* [clawker container inspect](clawker_container_inspect.md) - Display detailed information on one or more containers
* [clawker container kill](clawker_container_kill.md) - Kill one or more running containers
* [clawker container list](clawker_container_list.md) - List containers
* [clawker container logs](clawker_container_logs.md) - Fetch the logs of a container
* [clawker container pause](clawker_container_pause.md) - Pause all processes within one or more containers
* [clawker container remove](clawker_container_remove.md) - Remove one or more containers
* [clawker container rename](clawker_container_rename.md) - Rename a container
* [clawker container restart](clawker_container_restart.md) - Restart one or more containers
* [clawker container run](clawker_container_run.md) - Create and run a new container
* [clawker container start](clawker_container_start.md) - Start one or more stopped containers
* [clawker container stats](clawker_container_stats.md) - Display a live stream of container resource usage statistics
* [clawker container stop](clawker_container_stop.md) - Stop one or more running containers
* [clawker container top](clawker_container_top.md) - Display the running processes of a container
* [clawker container unpause](clawker_container_unpause.md) - Unpause all processes within one or more containers
* [clawker container update](clawker_container_update.md) - Update configuration of one or more containers
* [clawker container wait](clawker_container_wait.md) - Block until one or more containers stop, then print their exit codes

### Options

```
  -h, --help   help for container
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
