# User Settings (settings.yaml)

`settings.yaml` is a **separate schema** from project config. It controls
clawker CLI behavior and preferences — NOT project build, agent, or container
configuration.

## Key differences from project config

| | Project config (`clawker.yaml`) | User settings (`settings.yaml`) |
|---|---|---|
| **Schema** | Project schema (build, agent, security, etc.) | Settings schema (separate, unrelated fields) |
| **Discovery** | Walk-up from CWD, multi-layer merge | Single file, no layering |
| **Inheritance** | User-level inherited by all projects | N/A — not project-scoped |
| **Location** | CWD, parent dirs, `~/.config/clawker/` | `~/.config/clawker/` only |

## When to consult settings

- User asks about clawker CLI preferences or behavior
- Troubleshooting issues with the clawker CLI itself (not container behavior)
- User wants to change how clawker operates on their machine

## When NOT to consult settings

- User asks about container build, MCP setup, firewall, or agent config —
  these are all project config
- User asks about config inheritance or layering — settings doesn't participate

## How to get the current schema

**Never guess at settings field names or types.** The settings schema is
deterministically documented at:

`https://docs.clawker.dev/configuration#user-settings-reference`

**Always fetch this page** before recommending any settings changes. Field
names, types, and available options change over time.

## File location

Settings live at `~/.config/clawker/settings.yaml`. The user can edit them
interactively via `clawker settings edit`.
