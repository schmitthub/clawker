# Clawker Support Plugin — Development Guide

## Core Principle: No Concrete Details

This skill intentionally avoids concrete configuration details. Configs,
packages, APIs, CLI flags, and tooling evolve constantly. Baking field names,
YAML examples, domain lists, or flag syntax into the skill produces stale
guidance that agents treat as authoritative.

**When concrete details DO appear, they are deliberate and load-bearing.**
They represent stable architectural concepts (e.g., `agent.post_init` as the
build-time vs runtime boundary). Do not add new concrete details without
considering whether they will go stale. If they will, point to a docs URL
instead.

### What belongs in skill files

- Decision frameworks and methodology (how to think about a problem)
- Workflow steps and interview questions
- Pointers to live documentation URLs
- Architectural concepts that are stable (discovery rules, layering model)
- Gotchas about common mistakes

### What does NOT belong in skill files

- Config field names (unless architecturally load-bearing)
- YAML syntax examples with real values
- Package names, version numbers, base image names
- CLI flag syntax (point to docs instead)
- Domain lists (hardcoded firewall domains, registry URLs)
- Anything derivable from `https://docs.clawker.dev/configuration`

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

## Relationship to Clawker Codebase

This plugin lives inside the clawker repo but is consumed independently by
Claude Code users. Changes to clawker's config schema, CLI commands, or
architecture may require updates here — but the fix is always to update
methodology and docs URLs, never to hardcode the new field names.

The `Dockerfile.tmpl` in `reference/` is the actual template from
`internal/bundler/`. If clawker's template changes, this copy should be
updated to match. A pre-commit hook (`Plugin Dockerfile.tmpl drift check`)
catches drift when both files are in the same commit.
