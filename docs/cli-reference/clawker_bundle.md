---
title: "clawker bundle"
---

## clawker bundle

Manage distributed bundles of harnesses, stacks, and monitoring extensions

### Synopsis

Manage clawker bundles — distribution units that ship one or more
harnesses, stacks, or monitoring extensions.

A bundle is declared in a clawker.yaml 'bundles:' entry (a git source or a local
path) and its content is fetched into a host-global cache. Bundled components are
addressed by their qualified namespace.bundle.component name; the embedded floor
and loose convention directories provide bare-named components.

### Examples

```
  # List every resolvable component and its provenance
  clawker bundle list

  # Validate a bundle directory before publishing
  clawker bundle validate ./my-bundle

  # Declare and fetch a bundle
  clawker bundle install https://github.com/acme/tools.git --ref v1.2.0
```

### Subcommands

* [clawker bundle install](clawker_bundle_install) - Declare a bundle source and fetch its content
* [clawker bundle list](clawker_bundle_list) - List bundles and their declaration↔cache state
* [clawker bundle prune](clawker_bundle_prune) - Remove cache entries no declaration addresses
* [clawker bundle remove](clawker_bundle_remove) - Purge a cached bundle from the host cache
* [clawker bundle update](clawker_bundle_update) - Refetch a cached bundle when its source version changed
* [clawker bundle validate](clawker_bundle_validate) - Validate a bundle directory

### Options

```
  -h, --help   help for bundle
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
