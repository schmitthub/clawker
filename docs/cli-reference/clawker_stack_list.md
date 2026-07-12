---
title: "clawker stack list"
---

## clawker stack list

List resolvable stacks and their provenance

### Synopsis

Lists every stack a build can select in 'build.stacks', across all three
tiers: the embedded floor, loose convention directories, and installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.stack for a bundle stack), the owning bundle's version where
applicable, and the resolution source — a bundle stack names its bundle so it
traces back to a 'clawker bundle list' row. A stack that shadows a
farther tier marks the shadowed sources with '!'.

```
clawker stack list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all stacks
  clawker stack list

  # Machine-readable output
  clawker stack list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker stack](clawker_stack) - Inspect resolvable stacks
