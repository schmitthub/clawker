## clawker container cp

Copy files/folders between a container and the local filesystem

### Synopsis

Copy files/folders between a container and the local filesystem.

Use '-' as the destination to write a tar archive of the container source
to stdout. Use '-' as the source to read a tar archive from stdin and
extract it to a directory destination in a container.

When --agent is provided, container names in CONTAINER:PATH are resolved
as agent names (clawker.<project>.<agent>).

Container path format: CONTAINER:PATH
Local path format: PATH

```
clawker container cp [OPTIONS] CONTAINER:SRC_PATH DEST_PATH
  clawker container cp [OPTIONS] SRC_PATH CONTAINER:DEST_PATH [flags]
```

### Examples

```
  # Copy file from container using agent name
  clawker container cp --agent ralph:/app/config.json ./config.json

  # Copy file to container using agent name
  clawker container cp --agent ./config.json ralph:/app/config.json

  # Copy file from container by full name
  clawker container cp clawker.myapp.ralph:/app/config.json ./config.json

  # Copy file from local to container
  clawker container cp ./config.json clawker.myapp.ralph:/app/config.json

  # Copy directory from container to local
  clawker container cp --agent ralph:/app/logs ./logs

  # Stream tar from container to stdout
  clawker container cp --agent ralph:/app - > backup.tar
```

### Options

```
      --agent         Treat container names as agent names (resolves to clawker.<project>.<agent>)
  -a, --archive       Archive mode (copy all uid/gid information)
      --copy-uidgid   Copy UID/GID from source to destination (same as -a)
  -L, --follow-link   Always follow symbol link in SRC_PATH
  -h, --help          help for cp
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker container](clawker_container.md) - Manage containers
