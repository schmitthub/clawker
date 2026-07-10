---
title: "clawker bundle list"
---

## clawker bundle list

List resolvable components and their provenance

### Synopsis

Lists every resolvable component — harnesses, stacks, and monitoring
extensions — across all three tiers: the embedded floor, loose convention
directories, and installed bundles.

Each row shows the component address (bare for floor/loose, qualified
namespace.bundle.component for a bundle), its type, the owning bundle version
where applicable, the resolution source, and — for a component that shadows a
farther tier — the shadowed sources marked with '!'.

```
clawker bundle list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List all components
  clawker bundle list

  # Short form
  clawker bundle ls

  # Machine-readable output
  clawker bundle list --json
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

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
