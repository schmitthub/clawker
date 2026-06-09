---
title: "clawker firewall disable"
---

## clawker firewall disable

Disable firewall for a container

### Synopsis

Remove an agent container from per-container egress filtering.
Re-enable later with 'clawker firewall enable'.

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
