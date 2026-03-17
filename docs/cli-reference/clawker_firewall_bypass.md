---
title: "clawker firewall bypass"
---

## clawker firewall bypass

Temporarily bypass firewall for a container

### Synopsis

Grant a container unrestricted egress for a specified duration. After the
timeout elapses, firewall rules are automatically re-applied.

Use --stop to cancel an active bypass immediately.

```
clawker firewall bypass <duration> [flags]
```

### Examples

```
  # Bypass firewall for 30 seconds
  clawker firewall bypass 30s --agent dev

  # Bypass firewall for 5 minutes
  clawker firewall bypass 5m --agent dev

  # Stop an active bypass
  clawker firewall bypass --stop --agent dev
```

### Options

```
      --agent string   Agent name to identify the container
  -h, --help           help for bypass
      --stop           Stop an active bypass
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
