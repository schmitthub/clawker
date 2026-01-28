---
name: audit-memory
description: Audit all Claude memory and documentation files for staleness, symbol accuracy, and context budget.
disable-model-invocation: true
allowed-tools: Bash, Read, Glob, Grep
---

# Audit Memory

Perform a comprehensive documentation health audit across all Claude context files.

## Steps

### 1. Inventory

Glob all documentation files and build a summary table:

- `CLAUDE.md` (root)
- `internal/*/CLAUDE.md` and `pkg/*/CLAUDE.md`
- `.claude/rules/*.md`
- `.claude/memories/*.md`
- `.claude/prds/*.md`
- `.serena/memories/*.md`

For each file, report: **path**, **line count** (`wc -l`), **estimated tokens** (lines × 4).

Group into categories:
- **Always-loaded**: root `CLAUDE.md`, `.claude/rules/*.md`
- **Lazy-loaded**: `internal/*/CLAUDE.md`, `pkg/*/CLAUDE.md`
- **On-demand**: `.claude/memories/*.md`, `.claude/prds/*.md`
- **WIP tracking**: `.serena/memories/*.md`

### 2. Freshness Check

Run the freshness script and include its output:

```bash
bash scripts/check-claude-freshness.sh --no-color
```

### 3. Symbol Accuracy

For each `internal/*/CLAUDE.md` and `pkg/*/CLAUDE.md`:

1. Read the file
2. Extract all backtick-wrapped Go identifiers (pattern: single backtick-wrapped words matching `[A-Z][A-Za-z0-9]*` — exported symbols)
3. For each identifier, grep for it in `*.go` files in the same directory
4. Report:
   - **Missing**: identifiers documented but not found in Go source (renamed or deleted)
   - **Undocumented**: exported Go symbols (`^func [A-Z]`, `^type [A-Z]`, `^var [A-Z]`, `^const [A-Z]`) in the directory not mentioned in the CLAUDE.md

### 4. Rules Path Validation

For each `.claude/rules/*.md` file with a `paths:` frontmatter field:

1. Parse the glob patterns from `paths: ["glob1", "glob2"]`
2. Run `git ls-files '<glob>'` for each pattern
3. Flag rules where no files match any glob (dead rule)

### 5. Serena Memory Staleness

Read each `.serena/memories/*.md` file and flag:

- Files containing "COMPLETE", "Status: Complete", "Status: Done", or "DONE" — these track finished work and should be cleaned up
- Files with no updates in >30 days (check `git log --format=%at -1`)

### 6. Context Budget

Report totals against budgets:

| Category | Budget | Actual |
|----------|--------|--------|
| Root CLAUDE.md + all rules | < 1000 lines | ? |
| Each individual CLAUDE.md | < 200 lines | ? |
| Total always-loaded context | < 1500 lines | ? |

Flag any files exceeding their budget.

### 7. Recommendations

Output a prioritized action list using these categories:

- **DELETE**: Completed WIP memories in `.serena/memories/`
- **UPDATE**: Stale docs (from freshness check) or docs with missing/wrong symbols
- **TRIM**: Files exceeding context budget
- **ADD**: Packages in `internal/` or `pkg/` with Go files but no `CLAUDE.md`

Format each recommendation as:
```
[ACTION] path/to/file — reason
```

Sort by priority: DELETE > UPDATE > TRIM > ADD.
