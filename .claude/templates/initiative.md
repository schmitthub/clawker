# {{INITIATIVE_TITLE}}

<!-- Reusable template for multi-phase testing/development initiatives.
     Replace {{PLACEHOLDER}} markers with project-specific content.
     Instantiate by copying to .serena/memories/ with a descriptive name. -->

**Branch:** `{{BRANCH_NAME}}`
**Parent memory:** `{{PARENT_MEMORY}}` (omit if top-level)
**PRD Reference:** `{{PRD_PATH}}` (omit if none)

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: {{TASK_1_TITLE}} | `pending` | — |
| Task 2: {{TASK_2_TITLE}} | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run a single `code-reviewer` subagent to review this task's changes, then fix any findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

{{DOMAIN_DESCRIPTION}}

### Key Files

{{KEY_FILES_LIST}}

### Design Patterns

{{DESIGN_PATTERNS — e.g., FakeAPIClient function-field pattern, input-spy closures, composite fakes, etc.}}

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package

---

## Task 1: {{TASK_1_TITLE}}

**Creates/modifies:** {{FILES}}
**Depends on:** {{DEPENDENCIES}}

### Implementation Phase

{{IMPLEMENTATION_STEPS}}

### Acceptance Criteria

```bash
{{ACCEPTANCE_COMMANDS}}
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the {{INITIATIVE_NAME}} initiative. Read the Serena memory `{{MEMORY_NAME}}` — Task 1 is complete. Begin Task 2: {{TASK_2_TITLE}}."

---

## Task 2: {{TASK_2_TITLE}}

<!-- Repeat the Task structure above for each subsequent task. -->
