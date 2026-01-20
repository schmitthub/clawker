## clawker container update

Update configuration of one or more containers

### Synopsis

Update configuration of one or more containers.

This command updates the resource limits of containers that are already running
or have been created but not yet started.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container update [OPTIONS] [CONTAINER...] [flags]
```

### Examples

```
  # Update memory limit using agent name
  clawker container update --memory 512m --agent ralph

  # Update memory limit by full name
  clawker container update --memory 512m clawker.myapp.ralph

  # Update CPU limit
  clawker container update --cpus 2 --agent ralph

  # Update multiple resources
  clawker container update --cpus 1.5 --memory 1g --agent ralph

  # Update multiple containers
  clawker container update --memory 256m container1 container2
```

### Options

```
      --agent string                Agent name (resolves to clawker.<project>.<agent>)
      --blkio-weight uint16         Block IO (relative weight), between 10 and 1000, or 0 to disable
      --cpu-shares int              CPU shares (relative weight)
      --cpus float                  Number of CPUs
      --cpuset-cpus string          CPUs in which to allow execution (0-3, 0,1)
      --cpuset-mems string          MEMs in which to allow execution (0-3, 0,1)
  -h, --help                        help for update
  -m, --memory string               Memory limit (e.g., 512m, 1g)
      --memory-reservation string   Memory soft limit (e.g., 256m)
      --memory-swap string          Swap limit equal to memory plus swap: -1 to enable unlimited swap
      --pids-limit int              Tune container pids limit (set -1 for unlimited)
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker container](clawker_container.md) - Manage containers
