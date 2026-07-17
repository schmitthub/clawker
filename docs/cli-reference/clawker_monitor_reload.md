---
title: "clawker monitor reload"
---

## clawker monitor reload

Apply this project's monitoring extensions to the running stack

### Synopsis

Applies this project's selected monitoring extensions to the running
monitoring stack and restarts the collector.

'monitor reload' re-renders the stack config from the seeded-extension union
(including this project's current selection), then stops and removes the
OpenTelemetry Collector so compose recreates it with the regenerated config.
This is the disruptive counterpart to 'monitor up': use it after editing
`monitor.extensions` while the stack is running.

The collector restart briefly interrupts telemetry ingestion; agent containers
buffer and retry, but in-flight batches can be dropped.

```
clawker monitor reload [flags]
```

### Examples

```
  # Apply a monitor.extensions edit to the running stack
  clawker monitor reload
```

### Options

```
  -h, --help   help for reload
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
