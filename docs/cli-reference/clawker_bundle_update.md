---
title: "clawker bundle update"
---

## clawker bundle update

Refetch a cached bundle when its source version changed

### Synopsis

Refetches a cached bundle when its declared source version changed. With a
namespace.name argument, only that bundle is checked; with no argument, every
declared bundle is checked. A sha-pinned source never moves; a ref (branch/tag)
source is compared against its current tip, and an unpinned source against the
repository's default branch, refetched only on a change. A failed refetch
leaves the cached version serving.

```
clawker bundle update [namespace.name] [flags]
```

### Examples

```
  # Check and update one bundle
  clawker bundle update acme.tools

  # Check every declared bundle
  clawker bundle update
```

### Options

```
  -h, --help   help for update
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
