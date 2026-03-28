# User Settings (settings.yaml)

`settings.yaml` is a **separate schema** from project config. It manages
global clawker infrastructure: the services and daemons that clawker runs on
the host (logging, monitoring, host proxy, firewall lifecycle). It does NOT
control what goes into agent containers — build config, agent config, firewall
rules, and workspace settings are all project config in `(.)clawker.yaml`.

## Key differences from project config

| | Project config (`(.)clawker.yaml`) | User settings (`settings.yaml`) |
| --- | --- | --- |
| **Schema** | Project schema (build, agent, security, etc.) | Settings schema (separate, unrelated fields) |
| **Discovery** | Walk-up from CWD, multi-layer merge | Single file, no layering |
| **Inheritance** | User-level inherited by all projects | N/A — not project-scoped |
| **Location** | CWD, parent dirs, `~/.config/clawker/` | `~/.config/clawker/` only |

## When to consult settings

- User asks about clawker CLI preferences or global behavior
- Troubleshooting clawker infrastructure (logging, monitoring, host proxy)
- User wants to change how clawker's host-level services operate

## When NOT to consult settings

- User asks about container build, MCP setup, agent config, or firewall
  rules — these are all project config (`(.)clawker.yaml`)
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

## Troubleshooting

### Settings not taking effect

1. **Verify the file exists and is valid YAML**:
   ```bash
   cat ~/.config/clawker/settings.yaml
   ```

2. **Use the editor to inspect current state**: `clawker settings edit`
   shows the merged settings with current values.

3. **Settings vs project config confusion**: If the user is changing a
   setting but seeing no effect on container behavior, they may be editing
   the wrong file. Build, agent, firewall rules, and workspace config live
   in project config (`(.)clawker.yaml`), not settings. See
   `reference/project-config.md`.
