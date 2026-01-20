## clawker volume create

Create a volume

### Synopsis

Creates a new clawker-managed volume.

If no name is specified, Docker will generate a random name.
The volume will be labeled as a clawker-managed resource.

```
clawker volume create [OPTIONS] [VOLUME] [flags]
```

### Examples

```
  # Create a volume with a name
  clawker volume create myvolume

  # Create a volume with specific driver
  clawker volume create --driver local myvolume

  # Create a volume with driver options
  clawker volume create --driver local --opt type=tmpfs --opt device=tmpfs myvolume

  # Create a volume with labels
  clawker volume create --label env=test --label project=myapp myvolume
```

### Options

```
  -d, --driver string       Specify volume driver name (default "local")
  -h, --help                help for create
      --label stringArray   Set metadata for a volume
  -o, --opt stringArray     Set driver specific options
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker volume](clawker_volume.md) - Manage volumes
