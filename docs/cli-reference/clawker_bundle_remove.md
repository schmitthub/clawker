---
title: "clawker bundle remove"
---

## clawker bundle remove

Purge a cached bundle from the host cache

### Synopsis

Removes a cached bundle — every version content root plus the cache-internal
metadata — from the host bundle cache, identified by its dotted namespace.name.

Removal only purges the cache; it does not edit any clawker.yaml. A bundle that
is still declared in a 'bundles:' entry re-fetches on the next
'clawker bundle install' — remove reports when that is the case.

```
clawker bundle remove <namespace.name> [flags]
```

### Aliases

`remove`, `rm`

### Examples

```
  # Purge a cached bundle
  clawker bundle remove acme.tools

  # Short form
  clawker bundle rm acme.tools
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

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
