---
title: "clawker firewall refresh"
---

## clawker firewall refresh

Re-sync firewall rules from the current project config

### Synopsis

Re-read the current project's config (security.firewall.add_domains
and security.firewall.rules) and sync those rules into the firewall store —
the same sync that runs when a container starts, but without a restart.

This is how you apply yaml edits live: edit config, then run refresh.

Sync is add/update only (merge, keyed by dst:proto:port). Domains removed
from config are NOT pruned from the store — use `clawker firewall remove`
to delete a rule.

```
clawker firewall refresh [flags]
```

### Examples

```
  # Apply config egress edits without restarting a container
  clawker firewall refresh
```

### Options

```
  -h, --help   help for refresh
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
