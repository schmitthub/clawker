---
name: audit-memory
description: Check agent instructions, memory files, and project docs for old information, incorrect symbols, and context size. Use only when the user explicitly requests an audit of these files.
disable-model-invocation: true
---

# Audit Memory

Use this procedure only when the user explicitly requests an audit of agent
instructions, memory, or project documentation. Do not start an audit merely
because another task reads or changes those files.

Check the shared agent files and the active tool’s memory files. See [REFERENCE.md](REFERENCE.md) for package and memory checks.

## Steps

### 1. Inventory

List the source files below. Do not count a symbolic link as a second copy:

- `AGENTS.md` (root)
- `cmd/**/AGENTS.md`, `internal/**/AGENTS.md`, `test/**/AGENTS.md`, and `pkg/**/AGENTS.md`
- `controlplane/**/AGENTS.md`, `clawkerd/AGENTS.md`
- `.agents/skills/*/SKILL.md` and their sidecar files
- `.serena/memories/**/*.md`
- `docs/docs.json`, `docs/custom.css`, `docs/favicon.svg` — Mintlify site config, theme, and favicon
- `docs/*.mdx` — Hand-authored Mintlify pages
- `docs/cli-reference/*.md` — Auto-generated CLI reference (never edit directly; generated via Makefile, freshness checked in CI)

For each file, report: **path**, **line count** (`wc -l`), **estimated tokens** (`wc -c` / 4).

Check every `CLAUDE.md` link: its relative target must be the sibling `AGENTS.md`. Check the links inside `.claude/` and `.codex/` against `.agents/skills/agent-files/SKILL.md`.

Group files by the active tool's documented loading behavior:
- **Initial instructions**: root `AGENTS.md` through the tool's native entry path
- **Package instructions**: `cmd/**/AGENTS.md`, `internal/**/AGENTS.md`, `test/**/AGENTS.md`, `pkg/**/AGENTS.md`
- **On-demand**: `.agents/skills/*/SKILL.md` (loaded on description match) and `.serena/memories/**/*.md` (loaded through `mem:` references)
- **WIP tracking**: `.serena/memories/**/*.md`
- **Mintlify site**: `docs/docs.json`, `docs/custom.css`, `docs/*.mdx`, `docs/cli-reference/*.md`

### 2. Freshness Check

Run the freshness script and include its output:

```bash
bash scripts/check-agents-freshness.sh --no-color
```

### 3. Symbol Accuracy

For each package `AGENTS.md` found in the inventory:

1. Read the file
2. Extract all backtick-wrapped Go identifiers (pattern: single backtick-wrapped words matching `[A-Z][A-Za-z0-9]*` — exported symbols)
3. For each identifier, grep for it in `*.go` files in the same directory
4. Report:
   - **Missing**: identifiers documented but not found in Go source (renamed or deleted)
   - **Undocumented**: exported Go symbols (`^func [A-Z]`, `^type [A-Z]`, `^var [A-Z]`, `^const [A-Z]`) in the directory not mentioned in the AGENTS.md. **Exclude** `Test*` and `Benchmark*` functions — these don't belong in AGENTS.md.

### 4. Serena Graph Validation

1. Run `serena memories check` when the CLI is available; otherwise resolve every `mem:` reference by hand.
2. Confirm `core` links each domain memory and each referring line says what the target covers.
3. Flag behavioral or situational content in a memory; it belongs in a skill.

### 5. Auto Memory Audit

Check Serena's `.serena/memories/` graph and the active tool's memory directory when it is available. Skills and package `AGENTS.md` files are not auto memory. Claude Code uses `~/.claude/projects/*/memory/`. Do not assume that Codex uses that structure. If a memory source is unavailable, report that limit:

1. **MEMORY.md index completeness**: List all `.md` files in the directory. Flag any not referenced in `MEMORY.md` (unindexed).
2. **Broken links**: Check that every `(filename.md)` reference in `MEMORY.md` points to an existing file.
3. **Stale project memories**: Read each `project_*.md` and `firewall_*.md` file. Flag those whose descriptions no longer match reality (e.g., "ready for planning" when work is complete).
4. **Frozen dates**: Flag any `currentDate` or hardcoded date blocks in `MEMORY.md` — these rot.
5. **Context limits**: Check the active tool’s documented load limit. Claude Code loads the first 200 lines of its auto-memory index; do not apply that limit to other memory formats.

### 6. Mintlify Docs Consistency

1. Check if `docs/docs.json` navigation groups reference files that actually exist in `docs/` and `docs/cli-reference/`
2. Flag hand-authored pages (`docs/*.mdx`) that reference outdated commands or config keys (spot-check against `.clawker.yaml` schema and CLI command tree)
3. Skip `docs/cli-reference/*.md` for content accuracy — they are auto-generated

### 7. Serena Memory Staleness

Read `.serena/memories/memory_maintenance.md` and follow its memory graph rules. Check `.serena/memories/**/*.md` for broken `mem:` references, obsolete current guidance, and unsupported claims.

Retained history and research can describe completed work. Do not mark a file for deletion only because it contains a completion marker or is more than 30 days old. Check its purpose and links first.

### 8. Contradiction Detection

Check for contradictions between always-loaded context files:

1. **Root AGENTS.md vs Serena `conventions`**: Identify instructions that appear in both. Flag exact duplicates (wasted context) and conflicting statements.
2. **Root AGENTS.md vs global instructions** (for example, `$CODEX_HOME/AGENTS.md` or `~/.claude/CLAUDE.md`, when present): Check for conflicting behavioral directives (e.g., "pivot on tech debt" vs "surgical changes only").
3. **Within root AGENTS.md**: Flag repeated information (e.g., same fact stated twice in different sections).

### 9. Architecture and Design Accuracy

1. Identify changes in architecture, design, CLI commands, test harnesses, or test doubles from the freshness check output, git statuses, or commit messages
2. For each reference skill (`writing-tests`, `cli-output`, `dev-checks`, `agent-files`) and each design memory (`architecture`, `design`, `key-concepts`, `repo-structure`, `project-guide`):
   - Check for mentions of outdated components, patterns, or practices
   - Flag files that likely need updates based on the nature of the changes

### 10. Context Budget

Report totals against budgets:

| Category | Budget | Actual |
|----------|--------|--------|
| Root AGENTS.md + always-loaded rules (no paths:) | < 500 lines | ? |
| Total always-loaded (root + rules + global) | < 800 lines | ? |
| Each individual AGENTS.md | < 200 lines | ? |

Flag any files exceeding their budget.

### 11. Recommendations

Output a prioritized action list using these categories:

- **DELETE**: Completed WIP memories, stale auto-memory files
- **UPDATE**: Stale docs (from freshness check) or docs with missing/wrong symbols
- **FIX**: Contradictions between always-loaded files
- **TRIM**: Files exceeding context budget
- **SCOPE**: Always-loaded rules that should have `paths:` frontmatter
- **ADD**: Packages in `internal/` or `pkg/` with significant Go files but no `AGENTS.md`

Format each recommendation as:
```
[ACTION] path/to/file — reason
```

Sort by priority: DELETE > FIX > UPDATE > TRIM > SCOPE > ADD.
