---
title: "clawker bundle prune"
---

## clawker bundle prune

Remove cache entries no declaration addresses

### Synopsis

Sweeps the host bundle cache against every declared bundle source: the
current project's clawker.yaml layers, the user config-dir clawker.yaml, and
every registered project (including worktrees). A cache entry survives only
while some declaration's exact source value addresses it — an entry stranded
by an edited ref, a swapped url, or a removed 'bundles:' entry is deleted, as
is the stale duplicate left behind when a bundle's upstream renamed its
identity. Staging debris from interrupted installs is also reclaimed — an
entry whose replacement never completed is restored.

Hand-placed entries (no fetch receipt) are never pruned; purge those with
'clawker bundle remove'. When one bundle identity is cached from two or more
different repositories across projects, prune reports them for review.

```
clawker bundle prune [flags]
```

### Examples

```
  # Remove every stranded cache entry
  clawker bundle prune
```

### Options

```
  -h, --help   help for prune
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
