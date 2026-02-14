## clawker container wait

Block until one or more containers stop, then print their exit codes

### Synopsis

Blocks until one or more clawker containers stop, then prints their exit codes.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container names can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container wait [OPTIONS] CONTAINER [CONTAINER...] [flags]
```

### Examples

```
  # Wait for a container using agent name
  clawker container wait --agent dev

  # Wait for a container by full name
  clawker container wait clawker.myapp.dev

  # Wait for multiple containers
  clawker container wait clawker.myapp.dev clawker.myapp.writer
```

### Options

```
      --agent   Use agent name (resolves to clawker.<project>.<agent>)
  -h, --help    help for wait
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
