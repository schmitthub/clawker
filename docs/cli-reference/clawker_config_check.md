## clawker config check

Validate your clawker configuration from the current project's context

### Synopsis

Validates the clawker configuration from this project's context.

Checks resolution and validation between $CLAWKER_HOME/settings.yaml, $CLAWKER_HOME/clawker.yaml, and ./clawker.yaml:
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
