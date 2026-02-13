## clawker config check

Validate clawker.yaml configuration

### Synopsis

Validates the clawker.yaml configuration file in the current directory.

Checks for:
  - Required fields (version, project, build.image)
  - Valid field values and formats
  - File existence for referenced paths (dockerfile, includes)
  - Security configuration consistency

```
clawker config check [flags]
```

### Examples

```
  # Validate configuration in current directory
  clawker config check
```

### Options

```
  -h, --help   help for check
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker config](clawker_config.md) - Configuration management commands
