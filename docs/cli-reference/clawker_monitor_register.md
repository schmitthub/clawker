---
title: "clawker monitor register"
---

## clawker monitor register

Register a monitoring unit directory

### Synopsis

Registers a monitoring unit directory in the host-global registry
(settings.yaml).

The directory must contain a monitoring.yaml manifest plus the artifact
files it declares (index templates, ingest pipelines, dashboards). The
unit name defaults to the directory's base name; override it with --name.

The registry is host-global, so the path is always stored absolute.
Registration makes a unit available — it does not seed anything. Activate
it with 'clawker monitor enable `<name>`'.

Unit names are a flat namespace with no override semantics: a name held
by a built-in unit (shipped inside an embedded harness bundle) cannot be
registered at all — choose another name with --name. --force only updates
the path of your own existing registered entry.

```
clawker monitor register <path> [flags]
```

### Examples

```
  # Register a unit from a harness bundle's monitoring/ dir
  clawker monitor register ~/tools/codex-bundle/monitoring/codex-usage

  # Register under an explicit name
  clawker monitor register ./observability/codex --name codex-usage

  # Update an existing registration's path
  clawker monitor register /new/path/codex-usage --force
```

### Options

```
      --force         Update an existing registered entry's path
  -h, --help          help for register
      --name string   Registry name (defaults to the directory base name)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
