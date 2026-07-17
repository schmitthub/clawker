---
title: "clawker monitor extensions"
---

## clawker monitor extensions

List resolvable monitoring extensions and their provenance

### Synopsis

Lists every monitoring extension a project can select in
'monitor.extensions', across all three tiers: the embedded floor, loose
convention directories, and installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.extension for a bundle extension), the owning bundle's
version where applicable, and the resolution source — a bundle extension names
its bundle so it traces back to a 'clawker bundle list' row. An extension that
shadows a farther tier marks the shadowed sources with '!'.

Selected extensions are seeded onto the stack by 'clawker monitor up' (or
applied to a running stack by 'clawker monitor reload').

```
clawker monitor extensions [flags]
```

### Aliases

`extensions`, `ext`

### Examples

```
  # List all monitoring extensions
  clawker monitor extensions

  # Machine-readable output
  clawker monitor extensions --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for extensions
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
