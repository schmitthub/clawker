---
title: "clawker firewall up"
---

## clawker firewall up

Start the firewall stack

### Synopsis

Bring the Envoy + CoreDNS firewall stack up via the control plane.
Idempotent — safe to invoke while the stack is already running.

```
clawker firewall up [flags]
```

### Examples

```
  # Start the firewall stack
  clawker firewall up
```

### Options

```
  -h, --help   help for up
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
