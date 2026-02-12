## clawker container stats

Display a live stream of container resource usage statistics

### Synopsis

Display a live stream of container resource usage statistics.

When no containers are specified, shows stats for all running clawker containers.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container stats [OPTIONS] [CONTAINER...] [flags]
```

### Examples

```
  # Show live stats for all running containers
  clawker container stats

  # Show stats using agent name
  clawker container stats --agent ralph

  # Show stats for specific containers
  clawker container stats clawker.myapp.ralph clawker.myapp.writer

  # Show stats once (no streaming)
  clawker container stats --no-stream

  # Show stats once for a specific container
  clawker container stats --no-stream --agent ralph
```

### Options

```
      --agent       Treat arguments as agent name (resolves to clawker.<project>.<agent>)
  -h, --help        help for stats
      --no-stream   Disable streaming stats and only pull the first result
      --no-trunc    Do not truncate output
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
