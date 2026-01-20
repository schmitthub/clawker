## clawker create

Create a new container

### Synopsis

Create a new clawker container from the specified image.

The container is created but not started. Use 'clawker container start' to start it.
Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml. When --name is provided, it overrides this.

If IMAGE is not specified, clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag

```
clawker create [OPTIONS] IMAGE [COMMAND] [ARG...] [flags]
```

### Examples

```
  # Create a container with a specific agent name
  clawker container create --agent myagent alpine

  # Create a container using default image from config
  clawker container create --agent myagent

  # Create a container with a command
  clawker container create --agent worker alpine echo "hello world"

  # Create a container with environment variables and ports
  clawker container create --agent web -e PORT=8080 -p 8080:8080 node:20

  # Create a container with a bind mount
  clawker container create --agent dev -v /host/path:/container/path alpine

  # Create an interactive container with TTY
  clawker container create -it --agent shell alpine sh
```

### Options

```
      --agent string          Agent name for container (uses clawker.<project>.<agent> naming)
      --entrypoint string     Overwrite the default ENTRYPOINT
  -e, --env stringArray       Set environment variables
  -h, --help                  help for create
  -i, --interactive           Keep STDIN open even if not attached
  -l, --label stringArray     Set metadata on container
      --mode string           Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)
      --name string           Full container name (overrides --agent)
      --network string        Connect container to a network
  -p, --publish stringArray   Publish container port(s) to host
      --rm                    Automatically remove container when it exits
  -t, --tty                   Allocate a pseudo-TTY
  -u, --user string           Username or UID
  -v, --volume stringArray    Bind mount a volume
  -w, --workdir string        Working directory inside the container
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
