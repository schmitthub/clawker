---
title: "clawker firewall"
---

## clawker firewall

Manage the egress firewall

### Synopsis

Manage the Envoy+CoreDNS egress firewall that controls outbound traffic
from agent containers.

The firewall runs as shared infrastructure on the clawker Docker network,
enforcing domain-level egress rules via Envoy (TLS SNI filtering) and
CoreDNS (DNS-level allow/deny).

### Examples

```
  # Show firewall health and status
  clawker firewall status

  # List active egress rules
  clawker firewall list

  # Allow a new domain
  clawker firewall add registry.npmjs.org

  # Remove a domain
  clawker firewall remove registry.npmjs.org

  # Temporarily bypass firewall for an agent
  clawker firewall bypass 30s --agent dev
```

### Subcommands

* [clawker firewall add](clawker_firewall_add) - Add an egress rule
* [clawker firewall bypass](clawker_firewall_bypass) - Temporarily bypass firewall for a container
* [clawker firewall disable](clawker_firewall_disable) - Disable firewall for a container
* [clawker firewall enable](clawker_firewall_enable) - Enable firewall for a container
* [clawker firewall list](clawker_firewall_list) - List active egress rules
* [clawker firewall reload](clawker_firewall_reload) - Force-reload firewall configuration
* [clawker firewall remove](clawker_firewall_remove) - Remove an egress rule
* [clawker firewall rotate-ca](clawker_firewall_rotate-ca) - Rotate the firewall CA certificate
* [clawker firewall status](clawker_firewall_status) - Show firewall health and status

### Options

```
  -h, --help   help for firewall
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
