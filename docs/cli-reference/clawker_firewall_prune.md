---
title: "clawker firewall prune"
---

## clawker firewall prune

Reset firewall rules to the current project config

### Synopsis

Remove every egress rule from the firewall store, then re-sync the rules
the current project config defines — the harness egress floor plus
security.firewall.add_domains and security.firewall.rules. Rules added with
`clawker firewall add` that are not in config are removed.

The rule store is shared across projects: rules other projects contributed
are removed too, and only the current project's rules are re-synced. Other
projects restore their rules with `clawker firewall refresh` or on
their next container start.

With --all, every rule is removed and nothing is re-synced: agent containers
lose all allowed egress until rules are re-added.

If the re-sync fails after the wipe, the previous rules are restored — a
failed prune leaves the store unchanged.

Prompts for confirmation; pass --yes to skip the prompt (required in
non-interactive sessions).

```
clawker firewall prune [flags]
```

### Examples

```
  # Reset firewall rules to what the project config defines
  clawker firewall prune

  # Remove every rule, re-sync nothing
  clawker firewall prune --all

  # Non-interactive approval
  clawker firewall prune --yes
```

### Options

```
  -a, --all    Remove every rule without re-syncing from project config
  -h, --help   help for prune
  -y, --yes    Do not prompt for confirmation
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
