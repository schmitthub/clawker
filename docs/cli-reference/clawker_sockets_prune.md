---
title: "clawker sockets prune"
---

## clawker sockets prune

Remove socket grants that no declaration uses

### Synopsis

Resolve each stored harness and its current socket declarations. Remove
rows for harnesses that no longer resolve and rows for host sockets that the
resolved manifest no longer declares. With --all, remove every socket grant.

```
clawker sockets prune [flags]
```

### Examples

```
  # Remove grants that no current harness socket uses
  clawker sockets prune

  # Remove every socket grant without a prompt
  clawker sockets prune --all --yes
```

### Options

```
  -a, --all    Remove every socket grant
  -h, --help   help for prune
  -y, --yes    Do not prompt for confirmation
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker sockets](clawker_sockets) - Manage host socket bridge grants
