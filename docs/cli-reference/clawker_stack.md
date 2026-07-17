---
title: "clawker stack"
---

## clawker stack

Inspect resolvable stacks

### Synopsis

Commands for inspecting the stacks a build can select.

A stack is a reusable image fragment selected by name in 'build.stacks'. Bare
names resolve from the embedded floor and loose convention directories
(.clawker/stacks/`<name>`/ in a project, or the same path under the user config
directory); qualified namespace.bundle.stack names resolve from installed
bundles.

### Examples

```
  # List every resolvable stack and where it comes from
  clawker stack list
```

### Subcommands

* [clawker stack list](clawker_stack_list) - List resolvable stacks and their provenance

### Options

```
  -h, --help   help for stack
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
