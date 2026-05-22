---
title: "clawker volume prune"
---

## clawker volume prune

Remove unused agent volumes

### Synopsis

Removes unused clawker-managed agent volumes (volumes labeled with purpose=agent).

By default all agent volumes are pruned — workspace, config, AND command
history. Config and history volumes persist per-agent settings and shell
history across sessions, so they will be lost if the matching agent container
is not running at prune time. Infrastructure volumes (monitoring stack and
any other clawker-managed volumes) are preserved unless --all is set.

For targeted cleanup, prefer 'clawker volume list' + 'clawker volume remove'.
Use with caution as this will permanently delete data.

```
clawker volume prune [OPTIONS] [flags]
```

### Examples

```
  # Remove unused agent volumes (workspace, config, history)
  clawker volume prune

  # Also remove infrastructure volumes (monitoring stack, etc.)
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
