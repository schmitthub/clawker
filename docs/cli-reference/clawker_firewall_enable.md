---
title: "clawker firewall enable"
---

## clawker firewall enable

Enable firewall for a container

### Synopsis

Re-attach eBPF cgroup programs to an agent container, restoring egress
restrictions. Use after 'clawker firewall disable'.

```
clawker firewall enable [flags]
```

### Examples

```
  # Enable firewall for an agent container
  clawker firewall enable --agent dev
```

### Options

```
      --agent string   Agent name to identify the container
  -h, --help           help for enable
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
