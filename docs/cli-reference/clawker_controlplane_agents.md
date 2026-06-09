---
title: "clawker controlplane agents"
---

## clawker controlplane agents

List agents currently registered with the control plane

### Synopsis

List every agent currently registered with the control plane.

The thumbprint shown is the SHA-256 of the agent's certificate. Agents
are uniquely identified by the (project, agent_name) pair — agents with
the same name in different projects appear as separate rows.

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
