---
title: "clawker harness list"
---

## clawker harness list

List registered and built-in harnesses

### Synopsis

Lists every harness available in the current project: project-registered
harness bundles from clawker.yaml and the built-in harnesses shipped with
clawker.

A project registration that reuses a shipped harness's name shadows it — the
SHADOWS column flags that. A harnesses.`<name>` entry that only carries
per-harness init config (no path) is not a registration and is not listed.

```
clawker harness list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List harnesses
  clawker harness list

  # Names only
  clawker harness list -q

  # JSON output
  clawker harness list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display harness names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker harness](clawker_harness) - Manage harness bundles
