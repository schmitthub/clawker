---
title: "clawker monitor enable"
---

## clawker monitor enable

Activate a monitoring unit

### Synopsis

Activates a monitoring unit: its indexes, dashboards, and collector
routing are seeded into the monitoring stack on the next
'clawker monitor init && clawker monitor up'.

Every unit — including built-in ones like claude-code — is inactive until
enabled; seeding is a deliberate choice, never automatic.

Resource exclusivity is checked here: enabling a unit whose index or
service.name route collides with an already-active unit fails, naming
the conflict. Disable the other unit first to swap loadouts.

```
clawker monitor enable <name> [flags]
```

### Examples

```
  # Seed Claude Code telemetry (index, dashboards, routing)
  clawker monitor enable claude-code

  # Then apply
  clawker monitor init && clawker monitor up
```

### Options

```
  -h, --help   help for enable
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
