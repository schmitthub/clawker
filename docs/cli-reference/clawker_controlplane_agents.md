---
title: "clawker controlplane agents"
---

## clawker controlplane agents

List agents currently registered with the control plane

### Synopsis

Snapshot every agent the CLI has registered with the control plane.

The CLI is the sole writer of the agent registry — entries are written
at container creation time alongside auth material delivery. This
command reads the registry sqlite database directly off the host
filesystem and works whether or not the control plane is running.

Identity is channel-bound: the certificate thumbprint shown here is the
SHA-256 over the agent's mTLS leaf cert. Agents are uniquely identified
by the composite (project, agent_name) — agents with the same short
name in different projects appear as separate rows.

```
clawker controlplane agents [flags]
```

### Examples

```
  # Show all registered agents
  clawker controlplane agents

  # Machine-readable output
  clawker controlplane agents --json
```

### Options

```
      --format string   Output format: "json", "table", or a Go template
  -h, --help            help for agents
      --json            Output as JSON (shorthand for --format json)
  -q, --quiet           Only display IDs
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker controlplane](clawker_controlplane) - Break-glass control plane lifecycle
