---
name: audit-memory
description: Audit all Claude memory and documentation files for staleness, symbol accuracy, and context budget.
disable-model-invocation: true
---

# Audit Memory

Perform a comprehensive documentation health audit across all Claude context files.

## Steps

### 1. Inventory

Glob all documentation files and build a summary table:

- `CLAUDE.md` (root)
- `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, and `pkg/*/CLAUDE.md`
- `.claude/rules/*.md`
- `.claude/docs/*.md`
- `.serena/memories/*.md`
- `docs/docs.json`, `docs/custom.css`, `docs/favicon.svg` — Mintlify site config, theme, and favicon
- `docs/*.mdx` — Hand-authored Mintlify pages
- `docs/cli-reference/*.md` — Auto-generated CLI reference (never edit directly; generated via Makefile, freshness checked in CI)

For each file, report: **path**, **line count** (`wc -l`), **estimated tokens** (`wc -c` / 4).

Group into categories:
- **Always-loaded**: root `CLAUDE.md`, `.claude/rules/*.md` without `paths:` frontmatter
- **Path-scoped**: `.claude/rules/*.md` with `paths:` frontmatter (loaded only when matching files touched)
- **Lazy-loaded**: `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, `pkg/*/CLAUDE.md`
- **On-demand**: `.claude/docs/*.md`
- **WIP tracking**: `.serena/memories/*.md`
- **Mintlify site**: `docs/docs.json`, `docs/custom.css`, `docs/*.mdx`, `docs/cli-reference/*.md`

### 2. Freshness Check

Run the freshness script and include its output:

```bash
bash scripts/check-claude-freshness.sh --no-color
```

### 3. Symbol Accuracy

For each `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, and `pkg/*/CLAUDE.md`:

1. Read the file
2. Extract all backtick-wrapped Go identifiers (pattern: single backtick-wrapped words matching `[A-Z][A-Za-z0-9]*` — exported symbols)
3. For each identifier, grep for it in `*.go` files in the same directory
4. Report:
   - **Missing**: identifiers documented but not found in Go source (renamed or deleted)
   - **Undocumented**: exported Go symbols (`^func [A-Z]`, `^type [A-Z]`, `^var [A-Z]`, `^const [A-Z]`) in the directory not mentioned in the CLAUDE.md. **Exclude** `Test*` and `Benchmark*` functions — these don't belong in CLAUDE.md.

### 4. Rules Path Validation

For each `.claude/rules/*.md` file with a `paths:` frontmatter field:

1. Parse the glob patterns from `paths: ["glob1", "glob2"]`
2. Run `git ls-files '<glob>'` for each pattern
3. Flag rules where no files match any glob (dead rule)
4. Flag rules WITHOUT `paths:` frontmatter — these are always-loaded and should be scoped if possible

### 5. Auto Memory Audit

Check the auto memory directory (`~/.claude/projects/*/memory/`):

1. **MEMORY.md index completeness**: List all `.md` files in the directory. Flag any not referenced in `MEMORY.md` (unindexed).
2. **Broken links**: Check that every `(filename.md)` reference in `MEMORY.md` points to an existing file.
3. **Stale project memories**: Read each `project_*.md` and `firewall_*.md` file. Flag those whose descriptions no longer match reality (e.g., "ready for planning" when work is complete).
4. **Frozen dates**: Flag any `currentDate` or hardcoded date blocks in `MEMORY.md` — these rot.
5. **Content beyond 200 lines**: Flag if `MEMORY.md` exceeds 200 lines (only first 200 loaded per session).

### 6. Mintlify Docs Consistency

1. Check if `docs/docs.json` navigation groups reference files that actually exist in `docs/` and `docs/cli-reference/`
2. Flag hand-authored pages (`docs/*.mdx`) that reference outdated commands or config keys (spot-check against `.clawker.yaml` schema and CLI command tree)
3. Skip `docs/cli-reference/*.md` for content accuracy — they are auto-generated

### 7. Serena Memory Staleness

Read each `.serena/memories/*.md` file and flag:

- Files containing "COMPLETE", "Status: Complete", "Status: Done", or "DONE" — these track finished work and should be cleaned up
- Files with no updates in >30 days (check `git log --format=%at -1`)

### 8. Contradiction Detection

Check for contradictions between always-loaded context files:

1. **Root CLAUDE.md vs .claude/rules/*.md**: Identify instructions that appear in both. Flag exact duplicates (wasted context) and conflicting statements.
2. **Root CLAUDE.md vs global CLAUDE.md** (`~/.claude/CLAUDE.md`): Check for conflicting behavioral directives (e.g., "pivot on tech debt" vs "surgical changes only").
3. **Within root CLAUDE.md**: Flag repeated information (e.g., same fact stated twice in different sections).

### 9. Architecture and Design Accuracy

1. Identify changes in architecture, design, CLI commands, test harnesses, or test doubles from the freshness check output, git statuses, or commit messages
2. For each `.claude/docs/*.md` files:
   - Check for mentions of outdated components, patterns, or practices
   - Flag files that likely need updates based on the nature of the changes

### 10. Context Budget

Report totals against budgets:

| Category | Budget | Actual |
|----------|--------|--------|
| Root CLAUDE.md + always-loaded rules (no paths:) | < 500 lines | ? |
| Total always-loaded (root + rules + global) | < 800 lines | ? |
| Each individual CLAUDE.md | < 200 lines | ? |

Flag any files exceeding their budget.

### 11. Recommendations

Output a prioritized action list using these categories:

- **DELETE**: Completed WIP memories, stale auto-memory files
- **UPDATE**: Stale docs (from freshness check) or docs with missing/wrong symbols
- **FIX**: Contradictions between always-loaded files
- **TRIM**: Files exceeding context budget
- **SCOPE**: Always-loaded rules that should have `paths:` frontmatter
- **ADD**: Packages in `internal/` or `pkg/` with significant Go files but no `CLAUDE.md`

Format each recommendation as:
```
[ACTION] path/to/file — reason
```

Sort by priority: DELETE > FIX > UPDATE > TRIM > SCOPE > ADD.
