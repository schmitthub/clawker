---
title: "clawker firewall refresh"
---

## clawker firewall refresh

Re-sync firewall rules from the current project config

### Synopsis

Re-read the current project's clawker.yaml (security.firewall.add_domains
and security.firewall.rules) and sync those rules into the firewall store —
the same sync that runs when a container starts, but without a restart.

This is how you apply yaml edits live: edit clawker.yaml, then run refresh.

How this differs from related commands:

  refresh  re-reads the project's clawker.yaml into the rule store, then
           reloads the stack if anything changed (this command).
  reload   regenerates Envoy/CoreDNS config from the CURRENT store state —
           it does NOT re-read clawker.yaml.
  add      applies a single ad-hoc rule from CLI flags.

Sync is add/update only (merge, keyed by dst:proto:port). Domains removed
from clawker.yaml are NOT pruned from the store — use `clawker firewall remove`
to delete a rule.

```
clawker firewall refresh [flags]
```

### Examples

```
  # Apply clawker.yaml egress edits without restarting a container
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
