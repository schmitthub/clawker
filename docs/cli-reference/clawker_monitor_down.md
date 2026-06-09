---
title: "clawker monitor down"
---

## clawker monitor down

Stop the monitoring stack

### Synopsis

Stops the monitoring stack using Docker Compose.

This stops and removes all monitoring containers.

Without --volumes, OpenSearch data + the bootstrap-applied
configuration (index templates, ISM policies, Dashboards index
patterns) persist in the named volume across restarts. With
--volumes the volumes are wiped and 'monitor up' re-runs the
clawker-opensearch-bootstrap container to reapply everything from
templates — this is the canonical way to pick up template edits,
since OpenSearch index templates only take effect at index creation.

```
clawker monitor down [flags]
```

### Examples

```
  # Stop the monitoring stack
  clawker monitor down

  # Stop and remove volumes
  clawker monitor down --volumes
```

### Options

```
  -h, --help      help for down
  -v, --volumes   Remove named volumes (next 'monitor up' re-runs bootstrap to reapply index templates, ISM policies, and Dashboards saved objects)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
