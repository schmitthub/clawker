# Clawker Support Plugin — Development Guide

See `../CLAUDE.md` for general Claude Code skill plugin conventions.

## Core Principle: Minimal Concrete Details

This skill avoids concrete configuration details where possible. Configs,
packages, APIs, CLI flags, and tooling evolve constantly. Baking field names,
domain lists, or flag syntax into the skill produces stale guidance that agents
treat as authoritative.

**When concrete details DO appear, they are deliberate and load-bearing.**
They represent either stable architectural concepts (e.g., `agent.post_init`
as the build-time vs runtime boundary) or curated reference samples that are
manually kept current.

### Reference config samples

`reference/sample-*.yaml` files contain working `.clawker.yaml` configs for
different stacks (Go, Node.js). These are standalone YAML files — not inlined
in markdown — so they only consume context when the agent reads them for a
relevant task. `project-config.md` has a table pointing to each sample.

Samples are manually maintained and NOT drift-checked. When updating, copy
from a known-working source. The docs site
(`https://docs.clawker.dev/configuration`) remains the authoritative schema
reference and should still be fetched for field-level details.

### What belongs in skill files

- Decision frameworks and methodology (how to think about a problem)
- Workflow steps and interview questions
- Pointers to live documentation URLs
- Architectural concepts that are stable (discovery rules, layering model)
- Gotchas about common mistakes
- Curated reference config samples (manually maintained, not drift-checked)

### What does NOT belong in skill files

- Exhaustive field name lists (point to docs instead)
- CLI flag syntax (point to docs instead)
- Domain lists (hardcoded firewall domains, registry URLs)
- Version numbers or base image digests

## Plugin Structure

```
claude-plugin/clawker-support/
├── .claude-plugin/plugin.json    # Plugin metadata and version
├── README.md                     # User-facing install and usage docs
├── CLAUDE.md                     # This file — development guide
└── skills/clawker-support/
    ├── SKILL.md                  # Main skill definition and workflow
    └── reference/
        ├── Dockerfile.tmpl       # Actual Go template (source of truth)
        ├── project-config.md     # Project config discovery, layering, troubleshooting
        ├── sample-go.yaml        # Reference config: Go project (clawker's own)
        ├── sample-node.yaml      # Reference config: Node.js project
        ├── settings.md           # User settings schema, troubleshooting
        ├── mcp-recipes.md        # MCP setup methodology, troubleshooting
        ├── troubleshooting.md    # Entry point routing to domain-specific sections
        └── known-issues.md       # Active bugs and workarounds
```

## Reference File Conventions

Each domain reference file (`project-config.md`, `settings.md`, `mcp-recipes.md`)
follows the same structure:

1. What it is and how it differs from other domains
2. How to get the current schema (always a docs URL)
3. Domain-specific methodology
4. **Troubleshooting** section (consistent heading name across all refs)

`troubleshooting.md` is the entry point — it has a routing table that points
to domain-specific troubleshooting sections and keeps only global/cross-cutting
diagnostics (clawker not found, firewall, credentials, container won't start).

## Versioning

Plugin version lives in `.claude-plugin/plugin.json`. Bump it for every
release-worthy change:

- **Patch** (0.x.Y): Typo fixes, wording improvements
- **Minor** (0.X.0): New reference files, workflow changes, structural refactors
- **Major** (X.0.0): Breaking changes to skill behavior or methodology

## Completion Gate

After making changes to the plugin:

1. Check that `known-issues.md` is still accurate — remove entries for fixed bugs
2. Verify reference file cross-references are consistent (troubleshooting routing
   table, SKILL.md research step references)
3. Bump the version in `plugin.json` if the change is user-visible

## Dockerfile Template Sync

The `Dockerfile.tmpl` in `reference/` is a copy of the actual template from
`internal/bundler/`. If clawker's template changes, this copy must be updated
to match. A pre-commit hook (`Plugin Dockerfile.tmpl drift check`) catches
drift when both files are in the same commit.

When updating the template, never hardcode new field names into the skill —
update methodology and docs URLs instead.
