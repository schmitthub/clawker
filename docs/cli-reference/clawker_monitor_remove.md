---
title: "clawker monitor remove"
---

## clawker monitor remove

Remove a monitoring unit registration

### Synopsis

Removes a monitoring unit registration from the host-global registry
(settings.yaml), dropping its activation state with it.

Only registered units can be removed. Built-in units (shipped inside
embedded harness bundles) cannot be removed — deactivate them with
'clawker monitor disable' instead.

Already-seeded indexes and dashboards persist in the running stack until
'clawker monitor down --volumes && clawker monitor up'.

```
clawker monitor remove <name> [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Remove a registration
  clawker monitor remove codex-usage
```

### Options

```
  -h, --help   help for remove
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker monitor](clawker_monitor) - Manage local observability stack
