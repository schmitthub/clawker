---
title: "clawker controlplane up"
---

## clawker controlplane up

Start the control plane

### Synopsis

Bring the clawker control plane container up. Idempotent — safe to
invoke while the CP is already running.

This builds the CP image from embedded binaries if it's missing, ensures
auth material (CA + server cert + CLI client cert), creates the CP
container on clawker-net, and blocks until the aggregate /healthz probe
reports 200.

```
clawker controlplane up [flags]
```

### Examples

```
  # Start the control plane
  clawker controlplane up
```

### Options

```
  -h, --help   help for up
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker controlplane](clawker_controlplane) - Break-glass control plane lifecycle
