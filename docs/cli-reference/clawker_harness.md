---
title: "clawker harness"
---

## clawker harness

Inspect resolvable harnesses

### Synopsis

Commands for inspecting the coding-agent harnesses a build can target.

A harness is the agent runtime an image is built for, picked at build time with
'clawker build -t `<harness>`' and at run time with the '@:`<harness>`' selector.
Bare names resolve from the embedded floor and loose convention directories
(.clawker/harnesses/`<name>`/ in a project, or the same path under the user
config directory); qualified namespace.bundle.harness names resolve from
installed bundles.

### Examples

```
  # List every resolvable harness and where it comes from
  clawker harness list
```

### Subcommands

* [clawker harness list](clawker_harness_list) - List resolvable harnesses and their provenance

### Options

```
  -h, --help   help for harness
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
