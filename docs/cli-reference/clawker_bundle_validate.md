---
title: "clawker bundle validate"
---

## clawker bundle validate

Validate a bundle directory

### Synopsis

Validates a bundle directory before publishing: the .clawker-bundle/bundle.yaml
manifest must be present and well-formed with the required namespace and name,
its component convention directories are checked, and every component is
loaded through the same front door the consuming commands use — a harness,
stack, or monitoring manifest that would break at build or monitor time fails
here instead.

A malformed or missing manifest, a missing required field, a reserved
namespace, or an invalid component is a hard failure. Unknown top-level
directories (with typo suggestions) and empty convention directories are
advisory warnings; --strict turns every warning into a failure. Validation is
local — it never fetches.

```
clawker bundle validate <dir> [flags]
```

### Examples

```
  # Validate a bundle directory
  clawker bundle validate ./my-bundle

  # Treat warnings as failures (for CI / authors)
  clawker bundle validate ./my-bundle --strict
```

### Options

```
  -h, --help     help for validate
      --strict   Treat advisory warnings as validation failures
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker bundle](clawker_bundle) - Manage distributed bundles of harnesses, stacks, and monitoring extensions
