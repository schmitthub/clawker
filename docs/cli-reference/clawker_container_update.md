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
      --agent                      Use agent name (resolves to clawker.<project>.<agent>)
      --blkio-weight uint16        Block IO (relative weight), between 10 and 1000, or 0 to disable (default 0)
      --cpu-period int             Limit CPU CFS (Completely Fair Scheduler) period
      --cpu-quota int              Limit CPU CFS (Completely Fair Scheduler) quota
      --cpu-rt-period int          Limit the CPU real-time period in microseconds
      --cpu-rt-runtime int         Limit the CPU real-time runtime in microseconds
  -c, --cpu-shares int             CPU shares (relative weight)
      --cpus decimal               Number of CPUs
      --cpuset-cpus string         CPUs in which to allow execution (0-3, 0,1)
      --cpuset-mems string         MEMs in which to allow execution (0-3, 0,1)
  -h, --help                       help for update
  -m, --memory bytes               Memory limit
      --memory-reservation bytes   Memory soft limit
      --memory-swap bytes          Swap limit equal to memory plus swap: -1 to enable unlimited swap
      --pids-limit int             Tune container pids limit (set -1 for unlimited)
      --restart string             Restart policy to apply when a container exits
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
