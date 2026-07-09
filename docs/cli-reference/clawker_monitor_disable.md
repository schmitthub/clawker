---
title: "clawker monitor disable"
---

## clawker monitor disable

Deactivate a monitoring unit

### Synopsis

Deactivates a monitoring unit: the next 'clawker monitor init' stops
rendering its artifacts and collector routing, and its telemetry falls to
the collector's debug-only unrouted pipeline.

Already-applied cluster state (index templates, pipelines, the index and
its data, dashboards) persists until
'clawker monitor down --volumes && clawker monitor up'.

```
clawker monitor disable <name> [flags]
```

### Examples

```
  # Deactivate a unit, then re-render and restart
  clawker monitor disable claude-code
  clawker monitor init && clawker monitor up
```

### Options

```
  -h, --help   help for disable
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
