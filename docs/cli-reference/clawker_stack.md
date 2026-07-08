---
title: "clawker stack"
---

## clawker stack

Manage stack definitions

### Synopsis

Manage stack definitions in the project's clawker.yaml registry.

A stack is a reusable collection of Dockerfile instruction injections that
provision a dev-stack (e.g. node = nvm + Node LTS, python = uv + CPython).
Registration points a name at a stack definition directory on disk; the name
can then be declared under build.stacks (or a build.harnesses.`<name>`.stacks
overlay) to render it into an image.

Clawker ships built-in stacks that resolve without registration; a project
registration under the same name shadows the shipped definition.

### Examples

```
  # Register a stack definition directory
  clawker stack register ./stacks/my-rust

  # Register under an explicit name
  clawker stack register ./vendor/rustup --name rust

  # List registered and built-in stacks
  clawker stack list

  # Remove a project registration
  clawker stack remove my-rust
```

### Subcommands

* [clawker stack list](clawker_stack_list) - List registered and built-in stacks
* [clawker stack register](clawker_stack_register) - Register a stack definition directory
* [clawker stack remove](clawker_stack_remove) - Remove a stack registration

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
