---
title: "clawker firewall list"
---

## clawker firewall list

List active egress rules

### Synopsis

List all currently active egress rules enforced by the firewall.

```
clawker firewall list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all rules
  clawker firewall list

  # Output as JSON
  clawker firewall ls --json

  # Custom Go template
  clawker firewall ls --format '{{.Domain}} {{.Proto}}'
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
