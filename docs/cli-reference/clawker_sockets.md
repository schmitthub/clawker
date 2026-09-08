---
title: "clawker sockets"
---

## clawker sockets

Manage host socket bridge grants

### Synopsis

List, inspect, revoke, and prune approvals and denials for host
socket bridges that harnesses request. Socket access is granted only during
container start; this command does not add grants.

### Subcommands

* [clawker sockets info](clawker_sockets_info) - Show one socket bridge grant
* [clawker sockets list](clawker_sockets_list) - List stored socket bridge grants
* [clawker sockets prune](clawker_sockets_prune) - Remove socket grants that no declaration uses
* [clawker sockets revoke](clawker_sockets_revoke) - Revoke stored socket bridge grants

### Options

```
  -h, --help   help for sockets
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
