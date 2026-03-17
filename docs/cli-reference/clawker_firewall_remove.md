---
title: "clawker firewall remove"
---

## clawker firewall remove

Remove an egress rule

### Synopsis

Remove a domain from the firewall allow list. The change takes effect
immediately via hot-reload — no container restart required.

```
clawker firewall remove <domain> [flags]
```

### Examples

```
  # Remove a domain rule
  clawker firewall remove registry.npmjs.org

  # Remove an SSH rule
  clawker firewall remove git.example.com --proto ssh --port 22
```

### Options

```
  -h, --help           help for remove
      --port int       Port number
      --proto string   Protocol (tls, ssh, tcp) (default "tls")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
