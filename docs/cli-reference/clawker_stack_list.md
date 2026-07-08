---
title: "clawker stack list"
---

## clawker stack list

List registered and built-in stacks

### Synopsis

Lists every stack available in the current project: project-registered
stacks from clawker.yaml and the built-in stacks shipped with clawker.

A project registration that reuses a shipped stack's name shadows it — the
SHADOWS column flags that.

```
clawker stack list [flags]
```

### Aliases

`list`, `ls`

### Examples

```
  # List stacks
  clawker stack list

  # Names only
  clawker stack list -q

  # JSON output
  clawker stack list --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for list
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display stack names
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker stack](clawker_stack) - Manage stack definitions
