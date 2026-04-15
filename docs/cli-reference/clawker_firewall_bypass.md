---
title: "clawker firewall bypass"
---

## clawker firewall bypass

Temporarily bypass firewall for a container

### Synopsis

Grant a container unrestricted egress for a specified duration.

Calls FirewallBypass on the control plane, which sets the BPF bypass flag
and starts a server-side dead-man timer that automatically re-enables
enforcement when the timer fires. The timer survives CLI exit.

By default the command blocks with a countdown timer. Press Ctrl+C to
stop the bypass early (re-enables firewall). Press q/Esc to detach
(bypass remains active until the server-side timer fires).

Use --non-interactive to start bypass and return immediately (fire-and-forget).
Use --stop to cancel an active bypass immediately.

```
clawker firewall bypass <duration> [flags]
```

### Examples

```
  # Bypass firewall for 5 minutes (blocks with countdown)
  clawker firewall bypass 5m --agent dev

  # Bypass in background (fire-and-forget)
  clawker firewall bypass 5m --agent dev --non-interactive

  # Stop a background bypass (re-enables firewall immediately)
  clawker firewall bypass --stop --agent dev
```

### Options

```
      --agent string      Agent name to identify the container
  -h, --help              help for bypass
      --non-interactive   Start bypass in background (use --stop to cancel)
      --stop              Stop an active bypass (re-enables firewall)
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
