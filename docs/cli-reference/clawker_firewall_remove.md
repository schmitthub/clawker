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

  # Remove a single path rule from a domain entry (entry itself stays)
  clawker firewall remove api.example.com --path /v1
```

### Options

```
  -h, --help           help for remove
      --path string    Remove a single path rule by its stored path (exact string match); omit to remove the whole entry
      --port int       Port number
      --proto string   L7 protocol (legacy 'tls' value translated to 'https') (default "https")
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
