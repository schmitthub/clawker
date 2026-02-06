# Output Styling Framework Initiative

**Branch:** `a/output-styling`
**Parent memory:** `PRESENTATION-LAYER-DESIGN.md`

---

## Architecture

```
simple commands  →  f.IOStreams  →  iostream  →  lipgloss
monitor command  →  f.TUI        →  tui  →  iostream (palette only)  →  lipgloss
```

Two packages, **mutually exclusive per command** — no command ever imports both.

**`internal/iostream`** — Core output package. Every non-TUI command uses it via `f.IOStreams`.
**`internal/tui`** — Full-screen BubbleTea experiences. Only monitor uses it via `f.TUI`.

### Import Boundaries

| Package | Can import | Cannot import |
|---------|-----------|---------------|
| `internal/iostream` | `lipgloss`, stdlib | `bubbletea`, `bubbles`, `internal/tui` |
| `internal/tui` | `bubbletea`, `bubbles`, `internal/iostream` (palette only) | `lipgloss` |

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: iostream Core — Streams, Colors, Tokens | `pending` | — |
| Task 2: iostream Output — Tables, Messages, Renders | `pending` | — |
| Task 3: iostream Animation — Spinners, Progress Bars | `pending` | — |
| Task 4: iostream Utilities — Text, Layout, Time | `pending` | — |
| Task 5: tui Layer — BubbleTea Models & Program Runner | `pending` | — |
| Task 6: Documentation & Memory Updates | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. **Self-review**: Launch code review sub-agents. Fix findings before proceeding.
3. Update the Progress Tracker in this memory
4. Append key learnings
5. Present handoff prompt to user
6. Wait for user to start new conversation

---

## Full Plan

See `/home/claude/.claude/plans/happy-noodling-elephant.md` for complete task specifications.
