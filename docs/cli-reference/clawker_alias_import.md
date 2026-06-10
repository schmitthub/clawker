---
title: "clawker alias import"
---

## clawker alias import

Import aliases from the project config

### Synopsis

Import command aliases from the project config into user settings.

Reads the aliases key of the current project's config (clawker.yaml,
including local overrides) and copies each entry into settings.yaml,
where it becomes active. Entries that shadow a clawker command or fail
validation are skipped with a warning. Existing aliases are kept unless
--clobber is given.

Project aliases are never applied automatically — importing is always
an explicit action.

```
clawker alias import [flags]
```

### Examples

```
  # Import the project's shared aliases
  clawker alias import

  # Import and overwrite existing aliases
  clawker alias import --clobber
```

### Options

```
      --clobber   Overwrite existing aliases
  -h, --help      help for import
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker alias](clawker_alias) - Manage command aliases
