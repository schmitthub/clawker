---
title: "clawker volume prune"
---

## clawker volume prune

Remove unused agent volumes

### Synopsis

Removes unused clawker-managed agent volumes (volumes labeled with purpose=agent).

By default only agent volumes are pruned. Other clawker-managed volumes
(monitoring, firewall, control plane, etc.) are preserved unless --all is set.
Use with caution as this will permanently delete data.

```
clawker volume prune [OPTIONS] [flags]
```

### Examples

```
  # Remove unused agent volumes
  clawker volume prune

  # Remove all unused clawker-managed volumes (agent, monitoring, etc.)
  clawker volume prune --all

  # Remove without confirmation prompt
  clawker volume prune --force
```

### Options

```
  -a, --all     Remove all clawker-managed volumes (default: only agent volumes)
  -f, --force   Do not prompt for confirmation
  -h, --help    help for prune
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker volume](clawker_volume) - Manage volumes
