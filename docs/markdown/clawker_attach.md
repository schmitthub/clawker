## clawker attach

Attach local standard input, output, and error streams to a running container

### Synopsis

Attach local standard input, output, and error streams to a running container.

Use ctrl-p, ctrl-q to detach from the container and leave it running.
To stop a container, use clawker container stop.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker attach CONTAINER [flags]
```

### Examples

```
  # Attach to a container using agent name
  clawker container attach --agent ralph

  # Attach to a container by full name
  clawker container attach clawker.myapp.ralph

  # Attach without stdin (output only)
  clawker container attach --no-stdin --agent ralph

  # Attach with custom detach keys
  clawker container attach --detach-keys="ctrl-c" --agent ralph
```

### Options

```
      --agent                Treat argument as agent name (resolves to clawker.<project>.<agent>)
      --detach-keys string   Override the key sequence for detaching a container
  -h, --help                 help for attach
      --no-stdin             Do not attach STDIN
      --sig-proxy            Proxy all received signals to the process (default true)
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
