---
title: "clawker firewall add"
---

## clawker firewall add

Add an egress rule

### Synopsis

Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.

```
clawker firewall add <domain> [flags]
```

### Examples

```
  # Allow HTTPS traffic to a domain
  clawker firewall add registry.npmjs.org

  # Allow SSH traffic on a custom port
  clawker firewall add git.example.com --proto ssh --port 22

  # Allow plain TCP traffic
  clawker firewall add api.example.com --proto tcp --port 8080
```

### Options

```
  -h, --help           help for add
      --port int       Port number (default: protocol-specific)
      --proto string   Protocol (tls, ssh, tcp) (default "tls")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
