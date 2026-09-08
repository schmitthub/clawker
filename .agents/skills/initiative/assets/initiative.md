# {{INITIATIVE_TITLE}}

<!-- Fill this template through .agents/skills/initiative/SKILL.md.
     Replace every placeholder; remove unused optional fields and these comments.
     Repeat the tracker row and task section for each task. -->

**Memory:** `mem:{{MEMORY_NAME}}`
**Branch:** `{{BRANCH_NAME}}`
**Parent memory:** `mem:{{PARENT_MEMORY}}`
**PRD Reference:** `{{PRD_PATH}}` (omit if none)

## Objective and Scope

{{EXPECTED_RESULT_AND_SCOPE_BOUNDARIES}}

**Initiative complete when:** {{MEASURABLE_COMPLETION_CRITERIA}}

**Assumptions and constraints:** {{ASSUMPTIONS_AND_CONSTRAINTS}}

## Working Procedure

Read `.agents/skills/initiative/SKILL.md` before planning or continuing work.
Complete one task, verify it, record the result, and provide a handoff for a new
conversation. Do not start the next task after wrap-up.

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task {{TASK_NUMBER}}: {{TASK_TITLE}} | `pending` | — |

Statuses: `pending`, `in_progress`, `blocked`, `complete`.

## Key Learnings

Append findings that the next task needs, with the source or task number.

## Context for All Agents

### Background

{{DOMAIN_DESCRIPTION}}

### Key Files

{{KEY_FILES_LIST}}

### Design Patterns

{{RELEVANT_DESIGN_PATTERNS_AND_DECISIONS}}

### Applicable Instructions

{{RELEVANT_INSTRUCTION_PATHS}}

---

## Task {{TASK_NUMBER}}: {{TASK_TITLE}}

**Creates/modifies:** {{FILES}}
**Depends on:** {{DEPENDENCIES}}

### Implementation

{{IMPLEMENTATION_STEPS}}

### Acceptance Criteria

```bash
{{ACCEPTANCE_COMMANDS}}
```

{{EXPECTED_RESULTS_AND_ANY_MANUAL_CHECKS}}

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

Update these results, the tracker, and key learnings through the skill's wrap-up
procedure. Stop and provide the handoff below. If this is the final completed
task, replace the handoff with an initiative completion report.

### Handoff Prompt

{{PROMPT_WITH_INITIATIVE_MEMORY_BRANCH_TASK_STATUS_NEXT_ACTION_AND_LIMITS}}
