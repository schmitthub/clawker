# Clawker Support Plugin

An agent skills plugin that acts as a clawker internals expert — helping you set up, configure, troubleshoot, and extend [clawker](https://github.com/schmitthub/clawker) environments.

## What it does

The plugin ships two skills:

### clawker-support

When you invoke `/clawker-support`, your agent becomes a clawker configuration specialist that:

- **Researches** what you're trying to add (packages, MCP servers, tools, runtimes)
- **Reads** the actual Dockerfile template and config schema to understand how clawker works
- **Synthesizes** the exact YAML config you need, with firewall rules and all

It understands the full clawker system: Dockerfile generation, config layering, firewall architecture, injection points, build-time vs runtime, and common gotchas.

### harness-toolchain-dev

When you invoke `/harness-toolchain-dev`, your agent becomes a clawker extension-authoring specialist for building harness bundles (packaging a new coding-agent CLI) and toolchain definitions (reusable language-toolchain install fragments):

- **Knows the bundle format** — `harness.yaml` field-by-field (version resolvers, volumes, seeds, staging, egress floors), verified against clawker's validators
- **Knows the template contract** — the six block slots of `Dockerfile.harness.tmpl`, their user/shell context, cache rules, and the runtime PATH gotcha
- **Knows toolchain authoring** — definition format, placement semantics, the self-guarding idiom, and namespace collision rules
- **Designs egress floors adversarially** — minimal floors, path-scoped UGC-sink denial, in-container auth posture

## Install

```bash
# Install with the clawker CLI (recommended)
clawker skill install
```

Or manually:

```bash
# Add the marketplace
claude plugin marketplace add schmitthub/claude-plugins

# Install the plugin
claude plugin install clawker-support@schmitthub-plugins
```

## Usage

In your agent session:

```
/clawker-support how do I add the GitHub MCP to my container?
```

```
/clawker-support my container can't reach pypi.org
```

```
/clawker-support help me set up a Rust project with clawker
```

```
/harness-toolchain-dev help me package the opencode CLI as a clawker harness
```

```
/harness-toolchain-dev write a toolchain definition for deno
```

Or just ask about clawker — the skills trigger automatically: clawker-support on clawker-related questions, harness-toolchain-dev on harness/toolchain authoring tasks.

## Documentation

Full clawker documentation: [docs.clawker.dev](https://docs.clawker.dev)
