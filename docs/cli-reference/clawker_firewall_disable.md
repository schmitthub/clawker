---
title: "clawker firewall disable"
---

## clawker firewall disable

Disable firewall for a container

### Synopsis

Remove an agent container from the firewall's per-container routing.
BPF programs remain attached so re-enable is cheap; the fast path exits to
bypass on lookup miss.

```
clawker firewall disable [flags]
```

### Examples

```
  # Disable firewall for an agent container
  clawker firewall disable --agent dev
```

### Options

```
      --agent string   Agent name to identify the container
  -h, --help           help for disable
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
