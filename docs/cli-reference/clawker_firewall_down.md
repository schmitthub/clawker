---
title: "clawker firewall down"
---

## clawker firewall down

Stop the firewall daemon

### Synopsis

Send SIGTERM to the firewall daemon process. The daemon will gracefully
shut down the Envoy and CoreDNS containers before exiting.

No-op if the daemon is not running.

```
clawker firewall down [flags]
```

### Examples

```
  # Stop the firewall daemon
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
