## clawker config check

Validate your clawker configuration

### Synopsis

Validates a clawker.yaml configuration file.

Checks for:
  - Valid YAML syntax
  - Known configuration structure

```
clawker config check [flags]
```

### Examples

```
  # Validate configuration in current directory
  clawker config check

  # Validate a specific file
  clawker config check --file examples/go.yaml
```

### Options

```
  -f, --file string   Path to clawker.yaml file to validate
  -h, --help          help for check
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker config](clawker_config.md) - Configuration management commands
