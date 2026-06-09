---
title: "clawker firewall down"
---

## clawker firewall down

Tear down the firewall stack

### Synopsis

Stop the Envoy + CoreDNS firewall stack. Pending bypass timers
are cancelled.

No-op if the stack is already stopped.

```
clawker firewall down [flags]
```

### Examples

```
  # Tear down the firewall stack
  clawker firewall down
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

* [clawker firewall](clawker_firewall) - Manage the egress firewall
