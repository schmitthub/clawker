---
title: "clawker monitor list"
---

## clawker monitor list

List monitoring units

### Synopsis

Lists every monitoring unit on this host: built-in units shipped inside
embedded harness bundles, registered units from the host-global registry
(settings.yaml), and — when run inside a project — units shipped by the
project's registered harness bundles that are not yet registered
(discoverable; promote one with 'clawker monitor register `<path>`').

Only ACTIVE units are seeded into the monitoring stack.

```
clawker monitor list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List units
  clawker monitor list

  # Names only
  clawker monitor list -q

  # JSON output
  clawker monitor list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display unit names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
