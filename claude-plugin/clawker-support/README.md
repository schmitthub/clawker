# Clawker Support Plugin

A Claude Code plugin that acts as a clawker internals expert — helping you set up, configure, and troubleshoot [clawker](https://github.com/schmitthub/clawker) environments.

## What it does

When you invoke `/clawker-support`, Claude becomes a clawker configuration specialist that:

- **Researches** what you're trying to add (packages, MCP servers, tools, runtimes)
- **Reads** the actual Dockerfile template and config schema to understand how clawker works
- **Synthesizes** the exact YAML config you need, with firewall rules and all

It understands the full clawker system: Dockerfile generation, config layering, firewall architecture, injection points, build-time vs runtime, and common gotchas.

## Install

```bash
# Add the marketplace
claude plugin marketplace add schmitthub/claude-plugins

# Install the plugin
claude plugin install clawker-support@claude-plugins
```

## Usage

In any Claude Code session:

```
/clawker-support how do I add the GitHub MCP to my container?
```

```
/clawker-support my container can't reach pypi.org
```

```
/clawker-support help me set up a Rust project with clawker
```

Or just ask about clawker — the skill triggers automatically when it detects clawker-related questions.

## Documentation

Full clawker documentation: [docs.clawker.dev](https://docs.clawker.dev)
