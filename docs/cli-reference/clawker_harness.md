---
title: "clawker harness"
---

## clawker harness

Manage harness bundles

### Synopsis

Manage harness bundles in the project's clawker.yaml registry.

A harness bundle packages a coding-agent runtime (its Dockerfile fragment,
egress floor, volumes, seeds, and optionally its own stack definitions).
Registration points a name at a bundle directory on disk; the name then
selects that harness when building and running containers.

Clawker ships built-in harnesses (e.g. claude) that resolve without
registration; a project registration under the same name shadows the shipped
bundle.

### Examples

```
  # Register a harness bundle directory
  clawker harness register ./tools/codex-bundle

  # Register under an explicit name
  clawker harness register ./vendor/codex --name codex

  # List registered and built-in harnesses
  clawker harness list

  # Remove a project registration
  clawker harness remove codex
```

### Subcommands

* [clawker harness list](clawker_harness_list) - List registered and built-in harnesses
* [clawker harness register](clawker_harness_register) - Register a harness bundle directory
* [clawker harness remove](clawker_harness_remove) - Remove a harness registration

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
