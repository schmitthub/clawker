---
title: "clawker stack register"
---

## clawker stack register

Register a stack definition directory

### Synopsis

Registers a stack definition directory in the project's clawker.yaml.

The directory must contain a stack.yaml manifest and at least one Dockerfile
fragment (Dockerfile.stack-root.tmpl and/or Dockerfile.stack-user.tmpl). The
stack name defaults to the directory's base name; override it with --name.

The path is stored relative to the project root when the directory lives inside
it (so the registry entry stays portable within the project), otherwise as an
absolute path. Registering a name that is already registered fails unless
--force is given, which replaces the entry and reports the shadowed path.

```
clawker stack register <path> [flags]
```

### Examples

```
  # Register ./stacks/my-rust as "my-rust"
  clawker stack register ./stacks/my-rust

  # Register under an explicit name
  clawker stack register ./vendor/rustup --name rust

  # Replace an existing registration
  clawker stack register ./stacks/my-rust --force
```

### Options

```
      --force         Replace an existing registration
  -h, --help          help for register
      --name string   Registry name (defaults to the directory base name)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker stack](clawker_stack) - Manage stack definitions
