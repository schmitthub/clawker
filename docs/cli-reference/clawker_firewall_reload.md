---
title: "clawker firewall reload"
---

## clawker firewall reload

Force-reload firewall configuration

### Synopsis

Regenerate Envoy and CoreDNS configuration from the current rule state
and trigger a hot-reload. Use this after manual config file edits.

```
clawker firewall reload [flags]
```

### Examples

```
  # Reload firewall configuration
  clawker firewall reload
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

* [clawker firewall](clawker_firewall) - Manage the egress firewall
