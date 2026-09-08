---
title: "clawker sockets revoke"
---

## clawker sockets revoke

Revoke stored socket bridge grants

### Synopsis

Delete one stored grant, all grants for a harness name, or all socket
grant rows. Both approvals and denials are deleted. A deleted grant can cause a
new authorization prompt on the next container start.

```
clawker sockets revoke <id> | --harness <name> | --all [flags]
```

### Examples

```
  # Revoke one grant
  clawker sockets revoke 7

  # Revoke all grants for each harness named acme
  clawker sockets revoke --harness acme

  # Revoke all socket grants without a prompt
  clawker sockets revoke --all --yes
```

### Options

```
  -a, --all              Revoke all socket grants
      --harness string   Revoke all grants for this harness name
  -h, --help             help for revoke
  -y, --yes              Do not prompt for confirmation
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker sockets](clawker_sockets) - Manage host socket bridge grants
