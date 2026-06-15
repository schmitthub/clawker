---
title: "clawker changelog"
---

## clawker changelog

Show curated, user-facing changelog entries

### Synopsis

Show the curated changelog — the handful of releases that changed the user surface.

With no arguments, shows the entry for the running version. Use --version to
select a specific release, --all for the full history, or --since to show
everything released after a given version.

The changelog is fetched from GitHub; when offline, the last cached copy is used.

```
clawker changelog [flags]
```

### Examples

```
  # The current version's changelog entry
  clawker changelog

  # A specific version's entry
  clawker changelog --version v0.12.3

  # The full curated history
  clawker changelog --all

  # Everything since v0.10.0
  clawker changelog --since v0.10.0
```

### Options

```
      --all              Show the full curated changelog history
  -h, --help             help for changelog
      --since string     Show entries released after the given version (e.g. v0.10.0)
      --version string   Show the entry for a specific version (e.g. v0.12.3)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
