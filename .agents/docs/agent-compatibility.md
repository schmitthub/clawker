# Claude Code and Codex compatibility

Read this file when changing native settings or the shared file layout.
Project instructions must have one source that both tools can read.
`.agents/` contains harness-independent content. Native settings, reviewer
definitions, and hooks specific to one tool stay in that tool's directory.

## Instruction loading

Codex reads `AGENTS.md` from the repository root through its working directory.
Claude Code reads the sibling `CLAUDE.md` symbolic links. The shared root requires
both tools to read the shared rules in `.agents/rules/` and package
instructions along each target file's path. This explicit requirement
also covers files outside the initial working directory.
[Codex instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[Claude memory](https://code.claude.com/docs/en/memory)

Claude can also load rules through `.claude/rules`, a directory link to
`.agents/rules`. Its `paths` fields control loading by file path. Codex follows
the shared index's read instruction. Do not describe `.agents/rules` as a native
Codex loader. When a rule changes, update its `paths` field and the shared index.
Check both when changing a rule's scope.

## Skills, references, and memory

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

## Native hooks and reviewer settings

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

## Checks and limits

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
- Relative links in the root `AGENTS.md` and the top-level `.agents` files
  resolve.

The check does not require named directories, links, skills, or subagents.
The PR lint workflow also runs the checker's own tests in
`scripts/test_agent_compatibility.py` and the command-guard tests in
`.agents/hooks/test_go_commands.py`. None of these start an agent or run a
supplied shell command.

Static checks establish file structure. They do not prove runtime loading in a
new Claude or Codex session. Personal settings, installed plugins, and
available tools remain properties of the user's tool installation.
