---
title: "clawker harness list"
---

## clawker harness list

List resolvable harnesses and their provenance

### Synopsis

Lists every harness a build can target with 'clawker build -t `<harness>`',
across all three tiers: the embedded floor, loose convention directories, and
installed bundles.

Each row shows the selectable name (bare, or qualified
namespace.bundle.harness for a bundle harness), the owning bundle's version
where applicable, and the resolution source — a bundle harness names its
bundle so it traces back to a 'clawker bundle list' row. A harness that
shadows a farther tier marks the shadowed sources with '!'.

```
clawker harness list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all harnesses
  clawker harness list

  # Machine-readable output
  clawker harness list --json
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

* [clawker harness](clawker_harness) - Inspect resolvable harnesses
