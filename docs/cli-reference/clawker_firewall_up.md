---
title: "clawker firewall up"
---

## clawker firewall up

Start the firewall daemon

### Synopsis

Start the firewall daemon process in the background. This manages the Envoy+CoreDNS container
lifecycle, monitors their health, and auto-exits when no clawker containers are running.

Normally started automatically by container commands when firewall is enabled.
Can also be started manually for debugging or pre-warming.

```
clawker firewall up [flags]
```

### Examples

```
  # Start the firewall daemon in the background
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
