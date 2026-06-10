---
title: "clawker alias export"
---

## clawker alias export

Export aliases to the project config

### Synopsis

Export active command aliases into the project config's aliases key.

Writes the current alias set (disabled aliases excluded) into the
project's shared config file so teammates can adopt them with
'clawker alias import'. Local override files are never the target.
Aliases already present in the project config are kept unless
--clobber is given.

The project config's aliases key is a sharing vehicle only — project
aliases are never applied automatically.

```
clawker alias export [flags]
```

### Examples

```
  # Share your aliases with the team
  clawker alias export

  # Overwrite existing project aliases
  clawker alias export --clobber
```

### Options

```
      --clobber   Overwrite aliases already in the project config
  -h, --help      help for export
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
