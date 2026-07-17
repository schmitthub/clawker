---
title: "clawker bundle list"
---

## clawker bundle list

List bundles and their declaration↔cache state

### Synopsis

Lists every bundle the current configuration knows about, one row per
identity: installed and in-place bundles that resolve, declared sources that
were never fetched, and cached bundles no live declaration matches.

The components a bundle ships are listed by the per-type inventory commands —
'clawker harness list', 'clawker stack list', and 'clawker monitor extensions'.

```
clawker bundle list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List bundles
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
