---
title: "clawker controlplane status"
---

## clawker controlplane status

Show control plane health

### Synopsis

Report the health of the clawker control plane.

Probes `/healthz` on the CP's HealthPort and, if the CP is up,
best-effort queries the AdminService for firewall subsystem state.
Tolerates a stopped CP — in that case the firewall fields are omitted
and the CP is reported as down.

```
clawker controlplane status [flags]
```

### Examples

```
  # Show CP status
  clawker controlplane status

  # Output as JSON
  clawker controlplane status --json

  # Custom Go template
  clawker controlplane status --format '{{.ContainerRunning}}'
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

* [clawker controlplane](clawker_controlplane) - Break-glass control plane lifecycle
