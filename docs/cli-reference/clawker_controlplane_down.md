---
title: "clawker controlplane down"
---

## clawker controlplane down

Stop the control plane

### Synopsis

Stop and remove the clawker control plane container.

Sends SIGTERM to the CP, which drains its own firewall stack (Envoy +
CoreDNS) and flushes per-container eBPF state before exiting. No orphan
containers, no stale map entries.

```
clawker controlplane down [flags]
```

### Examples

```
  # Stop the control plane (and everything it owns)
  clawker controlplane down
```

### Options

```
  -h, --help   help for down
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker controlplane](clawker_controlplane) - Break-glass control plane lifecycle
