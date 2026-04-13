---
title: "clawker auth"
---

## clawker auth

Manage control plane authentication material

### Synopsis

Manage the CLI's authentication material used to communicate with the
clawker control plane. The CLI is the root of trust — it generates CA
certificates, signing keys, and server TLS certificates that are
bind-mounted into the CP container.

### Subcommands

* [clawker auth rotate](clawker_auth_rotate) - Rotate control plane auth material

### Options

```
  -h, --help   help for auth
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Manage Claude Code in secure Docker containers with clawker
