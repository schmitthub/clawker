## clawker container exec

Execute a command in a running container

### Synopsis

Execute a command in a running clawker container.

This creates a new process inside the container and connects to it.
Use -it flags for an interactive shell session.

When --agent is provided, the container name is resolved as clawker.<project>.<agent>
using the project from your clawker.yaml configuration.

Container name can be:
  - Full name: clawker.myproject.myagent
  - Container ID: abc123...

```
clawker container exec [OPTIONS] [CONTAINER] COMMAND [ARG...] [flags]
```

### Examples

```
  # Run a command
  clawker container exec clawker.myapp.ralph ls -la

  # Run a command using agent name (resolves via project config)
  clawker container exec --agent ralph ls -la

  # Run an interactive shell
  clawker container exec -it clawker.myapp.ralph /bin/bash

  # Run an interactive shell using agent name
  clawker container exec -it --agent ralph /bin/bash

  # Run with environment variable
  clawker container exec -e FOO=bar clawker.myapp.ralph env

  # Run as a specific user
  clawker container exec -u root clawker.myapp.ralph whoami

  # Run in a specific directory
  clawker container exec -w /tmp clawker.myapp.ralph pwd
```

### Options

```
      --agent string      Agent name (resolves to clawker.<project>.<agent>)
      --detach            Detached mode: run command in the background
  -e, --env stringArray   Set environment variables
  -h, --help              help for exec
  -i, --interactive       Keep STDIN open even if not attached
      --privileged        Give extended privileges to the command
  -t, --tty               Allocate a pseudo-TTY
  -u, --user string       Username or UID (format: <name|uid>[:<group|gid>])
  -w, --workdir string    Working directory inside the container
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
