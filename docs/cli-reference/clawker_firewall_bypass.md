---
title: "clawker firewall bypass"
---

## clawker firewall bypass

Temporarily bypass firewall for a container

### Synopsis

Grant a container unrestricted egress for a specified duration.

By default the command blocks with a countdown timer. Press Ctrl+C to
stop the bypass early. When the timer expires, firewall rules are
automatically re-applied.

Use --non-interactive to start the bypass in the background. In this
mode, use --stop to cancel an active bypass.

```
clawker firewall bypass <duration> [flags]
```

### Examples

```
  # Bypass firewall for 5 minutes (blocks with countdown)
  clawker firewall bypass 5m --agent dev

  # Bypass in background (fire-and-forget)
  clawker firewall bypass 5m --agent dev --non-interactive

  # Stop a background bypass
  clawker firewall bypass --stop --agent dev
```

### Options

```
      --agent string      Agent name to identify the container
  -h, --help              help for bypass
      --non-interactive   Start bypass in background (use --stop to cancel)
      --stop              Stop an active bypass
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker firewall](clawker_firewall) - Manage the egress firewall
