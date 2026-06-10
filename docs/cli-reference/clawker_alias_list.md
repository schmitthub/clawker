---
title: "clawker alias list"
---

## clawker alias list

List configured command aliases

### Synopsis

Lists all configured command aliases with their expansions.

The SOURCE column shows the config file providing the winning value,
or "default" for shipped defaults. An alias with an empty expansion
is disabled.

```
clawker alias list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List aliases
  clawker alias list

  # Output as JSON
  clawker alias list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display alias names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
