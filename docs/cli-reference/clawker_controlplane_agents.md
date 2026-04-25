---
title: "clawker controlplane agents"
---

## clawker controlplane agents

List agents currently registered with the control plane

### Synopsis

Snapshot every agent that has completed AgentService.Register.

Identity is channel-bound: the certificate thumbprint shown here is the
SHA-256 over the agent's mTLS leaf cert and is what the control plane
uses as the registry key.

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
