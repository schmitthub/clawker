---
title: "clawker firewall disable"
---

## clawker firewall disable

Disable firewall for a container

### Synopsis

Flush iptables DNAT rules and restore direct DNS in an agent container,
giving it unrestricted outbound access. Use 'clawker firewall enable' to re-apply.

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
