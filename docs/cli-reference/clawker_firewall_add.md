---
title: "clawker firewall add"
---

## clawker firewall add

Add an egress rule

### Synopsis

Add a domain to the firewall allow list. The rule takes effect immediately
via hot-reload — no container restart required.

Pass --path together with --action to add a path-scoped rule onto the domain
entry instead of (or alongside) the bare-domain allow. Path rules accumulate
across calls; a repeated --path with a different --action overwrites the
prior action for that path.

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

  # Add a path-scoped allow rule onto a domain entry
  clawker firewall add api.example.com --path /v1 --action allow
```

### Options

```
      --action string   Action for the path rule: allow or deny (requires --path)
  -h, --help            help for add
      --path string     URL path prefix for a path-scoped rule, matched as a prefix at request time (requires --action)
      --port int        Port number (default: protocol-specific)
      --proto string    L7 protocol: https (TLS-MITM, default), http (plaintext HCM), ssh, tcp, or any opaque L7 name (TCP pass-through) (default "https")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
