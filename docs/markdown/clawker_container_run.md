## clawker container run

Create and run a new container

### Synopsis

Create and run a new clawker container from the specified image.

Container names follow clawker conventions: clawker.project.agent

When --agent is provided, the container is named clawker.<project>.<agent> where
project comes from clawker.yaml.

If IMAGE is "@", clawker will use (in order of precedence):
1. default_image from clawker.yaml
2. default_image from user settings (~/.local/clawker/settings.yaml)
3. The project's built image with :latest tag

```
clawker container run [OPTIONS] IMAGE [COMMAND] [ARG...] [flags]
```

### Examples

```
  # Run an interactive shell
  clawker container run -it --agent shell @ alpine sh

  # Run using default image with generated agent name from config
  clawker container run -it @

  # Run a command
  clawker container run --agent worker @ echo "hello world"
  clawker container run --agent worker myimage:tag echo "hello world"

  # Pass a claude code flag
  clawker container run --detach --agent web @ -p "build entire app, don't make mistakes"

  # Run with environment variables
  clawker container run -it --agent dev -e NODE_ENV=development @ echo $NODE_ENV

  # Run with a bind mount
  clawker container run -it --agent dev -v /host/path:/container/path @

  # Run and automatically remove on exit
  clawker container run --rm -it @ sh
```

### Options

```
      --agent string          Assign a name to the agent, used in container name (mutually exclusive with --name)
      --detach                Run container in background and print container ID
      --entrypoint string     Overwrite the default ENTRYPOINT
  -e, --env stringArray       Set environment variables
  -h, --help                  help for run
  -i, --interactive           Keep STDIN open even if not attached
  -l, --label stringArray     Set metadata on container
      --mode string           Workspace mode: 'bind' (live sync) or 'snapshot' (isolated copy)
      --name string           Same as --agent; provided for Docker CLI familiarity (mutually exclusive with --agent)
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

* [clawker container](clawker_container.md) - Manage containers
