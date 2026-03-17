---
title: "clawker firewall status"
---

## clawker firewall status

Show firewall health and status

### Synopsis

Display the current health and configuration of the Envoy+CoreDNS
egress firewall, including container health, active rule count, and network info.

```
clawker firewall status [flags]
```

### Examples

```
  # Show firewall status
  clawker firewall status

  # Output as JSON
  clawker firewall status --json

  # Custom Go template
  clawker firewall status --format '{{.RuleCount}} rules active'
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for status
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
