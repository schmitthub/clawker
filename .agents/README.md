# Shared agent files

This directory contains harness-independent project content. Claude Code and
Codex use one set of project instructions. Start with the
[root AGENTS.md](../AGENTS.md); it names the rules, skills, and references
for the task. Each regular `AGENTS.md` has a sibling
`CLAUDE.md` link with the relative target `AGENTS.md`, including package instructions.

## File ownership

| Content | Shared source | Native access |
| --- | --- | --- |
| Project and package instructions | Root and package `AGENTS.md` | Claude reads sibling `CLAUDE.md` links; Codex reads `AGENTS.md` |
| Repository-wide rules | `rules/*.md` | Claude loads them through the `.claude/rules` link with `paths:`; Codex reads them because the root `AGENTS.md` requires it |
| Skills | `skills/*/SKILL.md` | Codex discovers the shared directory; `.claude/skills` links to it |
| Architecture and API references | `docs/` | Both follow shared links |
| Development templates | `templates/` | Both use the shared files |
| Durable project memory | `../.serena/memories/` | Both use Serena or read the shared memory files |
| Command guards | `hooks/` | Registered in both native configurations |
| Subagents | `skills/<name>/agents/claude.md`, `skills/<name>/agents/codex.toml` | `.claude/agents/<name>.md` links to the Claude file; `.codex/config.toml` names the Codex file |
| GitHub PR review | `../.github/copilot-instructions.md` and `../.github/skills/` | GitHub review instructions use the same rule directory |

Use the [initiative skill](skills/initiative/SKILL.md) to plan development or
testing across conversations and resume one task at a time. Its reusable
template lives in the skill's `assets/` directory. Plans and task results live
in the shared Serena memory graph.

Native configuration belongs to its harness:

- `.claude/settings.json`, `.claude/settings.local.json.example`, and Claude-only `.claude/hooks/` files are regular native files. `.claude/agents/` holds links to skill sidecars or Claude-only agents.
- `.codex/config.toml` contains Codex settings and names the Codex subagent files.
- Both native configurations call the shared command guards in `.agents/hooks/`.
- Harness metadata for a skill lives in that skill's `agents/` directory: `openai.yaml` for Codex skill policy, `claude.md` and `codex.toml` for subagent definitions.

## Maintenance rules

- Add project requirements once, in a shared file. Link to that source from both tools.
- Keep required rules separate from optional skill procedures. A rule file is repository-wide; a requirement for one package tree goes in that tree's `AGENTS.md`.
- Keep the root brief. Put commands in [development](docs/development.md), project details in [the project guide](docs/project-guide.md), and CP constraints in [the control-plane instructions](../controlplane/AGENTS.md#control-plane-safety).
- Keep native configuration and private state in `.claude/` or `.codex/`. Do not store them in `.agents/` through copies or links.
- When agents work at the same time, preserve current edits and use the shared memory graph for project knowledge.
- Read [the compatibility reference](docs/agent-compatibility.md) before changing loading conditions, hooks, or native reviewer settings.

## Verification

The `agent-compat` commit hook and the PR lint workflow run
`scripts/check-agent-compatibility.py`. Do not run it by hand before a commit;
the hook reports layout problems at commit time and CI reports them on the
pull request. See [the compatibility reference](docs/agent-compatibility.md)
for what it checks.

For instruction changes, check files and formats with `git diff --check` and
the advisory `bash scripts/check-agents-freshness.sh --no-color`. Do not run
application tests, install or run Claude Code, or make model API calls for
these checks.
