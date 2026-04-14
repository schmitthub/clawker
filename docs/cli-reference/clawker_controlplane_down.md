---
title: "clawker controlplane down"
---

## clawker controlplane down

Stop the control plane

### Synopsis

Stop and remove the clawker control plane container.

This does NOT stop the Envoy or CoreDNS firewall containers — they are
owned by the CP but live past CP shutdown. To tear the firewall down
first, run `clawker firewall down` BEFORE `clawker controlplane down`;
otherwise Envoy and CoreDNS will keep running as orphans on clawker-net
until the next `clawker controlplane up` adopts them.

```
clawker controlplane down [flags]
```

### Examples

```
  # Recommended teardown order
  clawker firewall down
  clawker controlplane down
```

### Options

```
  -h, --help   help for down
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker controlplane](clawker_controlplane) - Break-glass control plane lifecycle
