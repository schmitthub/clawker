---
title: "clawker harness register"
---

## clawker harness register

Register a harness bundle directory

### Synopsis

Registers a harness bundle directory in the project's clawker.yaml.

The directory must contain a harness.yaml manifest and a Dockerfile.harness.tmpl
fragment. The harness name defaults to the directory's base name; override it
with --name. Any stack definitions the bundle embeds under stacks/ are reported.

The path is stored relative to the project root when the directory lives inside
it, otherwise as an absolute path. Registering a name that is already registered
fails unless --force is given, which replaces the entry and reports the shadowed
path. Registration writes only the harnesses.`<name>`.path key, so any per-harness
init config on that entry is preserved.

```
clawker harness register <path> [flags]
```

### Examples

```
  # Register ./tools/codex-bundle as "codex-bundle"
  clawker harness register ./tools/codex-bundle

  # Register under an explicit name
  clawker harness register ./vendor/codex --name codex

  # Replace an existing registration
  clawker harness register ./tools/codex-bundle --name codex --force
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

* [clawker harness](clawker_harness) - Manage harness bundles
