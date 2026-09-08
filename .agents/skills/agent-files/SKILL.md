---
name: agent-files
description: Use when adding or changing AGENTS.md files, CLAUDE.md links, skills, subagents, command guards, Serena memories, or Claude Code and Codex settings in clawker.
---

# agent-files

This directory contains harness-independent project content. Claude Code and
Codex use one set of project instructions. Start with the
[root AGENTS.md](../../../AGENTS.md); it names the rules, skills, and references
for the task. Each regular `AGENTS.md` has a sibling
`CLAUDE.md` link with the relative target `AGENTS.md`, including package instructions.

## File ownership

- Project and package instructions: Root and package `AGENTS.md`. Claude reads sibling `CLAUDE.md` links; Codex reads `AGENTS.md`.
- Conventions and design knowledge: Serena memories under `.serena/memories/`. There is no rule directory; a mandatory constraint goes in the root or a package `AGENTS.md`.
- Skills: `skills/*/SKILL.md`. Codex discovers the shared directory; `.claude/skills` links to it.
- Development templates: `templates/`. Both use the shared files.
- Durable project memory: `../.serena/memories/`. Both use Serena or read the shared memory files.
- Command guards: `hooks/`. Registered in both native configurations.
- Subagents: `skills/<name>/agents/claude.md`, `skills/<name>/agents/codex.toml`. `.claude/agents/<name>.md` links to the Claude file; `.codex/config.toml` names the Codex file.
- GitHub PR review: `../.github/copilot-instructions.md` and `../.github/skills/`. GitHub review instructions use the same rule directory.

Use the [initiative skill](../initiative/SKILL.md) to plan development or
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
- Keep the root brief. Put commands in the [dev-checks skill](../dev-checks/SKILL.md), project details in the Serena `project-guide` memory, and CP constraints in [the control-plane instructions](../../../controlplane/AGENTS.md#control-plane-safety).
- Keep native configuration and private state in `.claude/` or `.codex/`. Do not store them in `.agents/` through copies or links.
- When agents work at the same time, preserve current edits and use the shared memory graph for project knowledge.
- Read the compatibility sections below before changing loading conditions, hooks, or native reviewer settings.

## Verification

The `agent-compat` commit hook and the PR lint workflow run
`scripts/check-agent-compatibility.py`. Do not run it by hand before a commit;
the hook reports layout problems at commit time and CI reports them on the
pull request. See the compatibility sections below
for what it checks.

For instruction changes, check files and formats with `git diff --check` and
the advisory `bash scripts/check-agents-freshness.sh --no-color`. Do not run
application tests, install or run Claude Code, or make model API calls for
these checks.

## Claude Code and Codex compatibility

Read this file when changing native settings or the shared file layout.
Project instructions must have one source that both tools can read.
`.agents/` contains harness-independent content. Native settings, reviewer
definitions, and hooks specific to one tool stay in that tool's directory.

### Instruction loading

Codex reads `AGENTS.md` from the repository root through its working directory.
Claude Code reads the sibling `CLAUDE.md` symbolic links. The shared root requires
both tools to read the Serena memories and package instructions along each
target file's path. This explicit requirement
also covers files outside the initial working directory.
[Codex instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[Claude memory](https://code.claude.com/docs/en/memory)

### Skills, references, and memory

Codex discovers repository skills under `.agents/skills`. Claude uses the same
files through `.claude/skills`. Add a skill once in the shared directory.
Required project rules must remain in the root or its required rule files;
optional skill discovery cannot be their only source.
[Codex skills](https://learn.chatgpt.com/docs/build-skills),
[Claude skills](https://code.claude.com/docs/en/skills)

The `audit-memory` description and procedure require an explicit user request.
Both tools receive that same instruction. This is a shared instruction, not a
native loading restriction. Keep vendor-specific invocation metadata out of
shared skills.

Both tools use `.agents/docs`, `.agents/templates`, and the shared Serena
memory graph under `.serena/memories`. Memory links use `mem:` references.
The root memory is `core`. Keep durable project knowledge there or in shared
references; do not make required knowledge depend on one tool's private memory.
The CLI output and prompter references are files in `.agents/docs`, not memories.

### Native hooks and reviewer settings

`.claude/settings.json` and `.codex/config.toml` are regular files in their
native directories. Both configurations register
one Git guard and one Go command guard from `.agents/hooks`. The Go guard reads
shell words and heredoc data without executing the supplied command. Python 3
is required for that guard; the structure checker requires Python 3.9 or later.

The Codex hooks use inline TOML. Do not add a `.codex/hooks.json` file: Codex
reads both sources and would register each guard twice. Project hook loading
requires a trusted project.
[Codex hook loading](https://learn.chatgpt.com/docs/config-file/config-advanced#hooks)

Claude's optional Serena hooks live in `.claude/hooks/`. Their registrations
are in `.claude/settings.local.json.example`.
The root `AGENTS.md` states the Serena procedure for both tools. Private
settings remain at their native paths and are not changed by this layout.
When updating private settings, remove duplicate Git or Go guard registrations;
the native project settings already register them.

A skill becomes a named subagent when its directory contains harness
definitions under `agents/`, next to the Codex skill policy file
`agents/openai.yaml`:

- `agents/claude.md` is a Claude Code subagent file. It preloads the skill with
  `skills: [<name>]`. Claude discovers subagents only in `.claude/agents`, so
  `.claude/agents/<name>.md` is a symbolic link to this file.
- `agents/codex.toml` is a Codex role file with `developer_instructions` that
  read the skill. Codex loads it through `[agents.<name>] config_file` in
  `.codex/config.toml`; the path is relative to `.codex` and must be a regular
  file, because Codex rejects a symbolic link there.

Keep the procedure in `SKILL.md`. The harness files carry only the name, the
description, tool limits, and the instruction to read the skill. Skills without
an `agents/` definition stay plain skills. Harness-only agents can still live in
`.claude/agents` or `.codex` as regular files.

### Checks and limits

The `agent-compat` commit hook and the PR lint workflow run
`scripts/check-agent-compatibility.py`. Agents do not run it by hand. It reads
tracked and untracked entries that Git does not ignore, and it validates what
exists:

- Each `AGENTS.md` is a regular file with a sibling `CLAUDE.md` symbolic link
  whose target is exactly `AGENTS.md`.
- Each symbolic link under `.agents`, `.claude`, and `.codex` is relative and
  resolves inside the repository. Links under `.agents` never resolve into a
  harness directory.
- Each `config_file` value in `.codex/config.toml` resolves to a regular file.
- Relative links in the root `AGENTS.md` and `.agents/skills` resolve.

The check does not require named directories, links, skills, or subagents.
The PR lint workflow also runs the checker's own tests in
`scripts/test_agent_compatibility.py` and the command-guard tests in
`.agents/hooks/test_go_commands.py`. None of these start an agent or run a
supplied shell command.

Static checks establish file structure. They do not prove runtime loading in a
new Claude or Codex session. Personal settings, installed plugins, and
available tools remain properties of the user's tool installation.
