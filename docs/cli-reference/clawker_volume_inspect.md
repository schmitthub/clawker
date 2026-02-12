## clawker volume inspect

Display detailed information on one or more volumes

### Synopsis

Returns low-level information about clawker volumes.

Outputs detailed volume information in JSON format.

```
clawker volume inspect VOLUME [VOLUME...] [flags]
```

### Examples

```
  # Inspect a volume
  clawker volume inspect clawker.myapp.ralph-workspace

  # Inspect multiple volumes
  clawker volume inspect clawker.myapp.ralph-workspace clawker.myapp.ralph-config
```

### Options

```
  -h, --help   help for inspect
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker volume](clawker_volume.md) - Manage volumes
