# Clawker Harness Dev Plugin

A Claude Code plugin for developers extending [clawker](https://github.com/schmitthub/clawker) — authoring harness bundles (packaging a new coding-agent CLI) and toolchain definitions (reusable language-toolchain install fragments).

## What it does

When you invoke `/clawker-harness-dev`, Claude becomes a clawker extension-authoring specialist that:

- **Knows the bundle format** — `harness.yaml` field-by-field (version resolvers, volumes, seeds, staging, egress floors), verified against clawker's validators
- **Knows the template contract** — the six block slots of `Dockerfile.harness.tmpl`, their user/shell context, cache rules, and the runtime PATH gotcha
- **Knows toolchain authoring** — definition format, placement semantics, the self-guarding idiom, and namespace collision rules
- **Designs egress floors adversarially** — minimal floors, path-scoped UGC-sink denial, in-container auth posture

This is the extension-author counterpart to the `clawker-support` plugin (end-user setup and troubleshooting).

## Install

```bash
# Add the marketplace
claude plugin marketplace add schmitthub/claude-plugins

# Install the plugin
claude plugin install clawker-harness-dev@schmitthub-plugins
```

## Usage

In any Claude Code session:

```
/clawker-harness-dev help me package the opencode CLI as a clawker harness
```

```
/clawker-harness-dev why does my harness.yaml fail with "dest is not under any declared volume path"?
```

```
/clawker-harness-dev write a toolchain definition for deno
```

Or just start working on a `harness.yaml` or `Dockerfile.harness.tmpl` — the skill triggers automatically on harness/toolchain authoring tasks.

## Documentation

Full clawker documentation: [docs.clawker.dev](https://docs.clawker.dev)
