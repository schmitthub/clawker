---
title: "clawker alias export"
---

## clawker alias export

Export aliases to the project config

### Synopsis

Export active command aliases into the project config's aliases key.

Writes the current alias set into the most local project config file
discovered in the walk-up, so the aliases are version-controlled with
the project. Export never creates a new file — it requires an existing
project config (see 'clawker init'). Disabled aliases and shipped
defaults are not exported, and entries the target file already
provides are left as they are.

```
clawker alias export [flags]
```

### Examples

```
  # Share your aliases with the team
  clawker alias export
```

### Options

```
  -h, --help   help for export
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
