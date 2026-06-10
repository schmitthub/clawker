---
title: "clawker controlplane up"
---

## clawker controlplane up

Start the control plane

### Synopsis

Bring the clawker control plane container up. Idempotent — safe to
invoke while the CP is already running.

On first run it builds the control plane image and provisions its auth
material, then waits until the control plane reports healthy.

When the firewall is enabled in settings.yaml (firewall.enable, the
default), the Envoy + CoreDNS firewall stack is brought up as well.

```
clawker controlplane up [flags]
```

### Examples

```
  # Start the control plane (and the firewall stack, per settings)
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
