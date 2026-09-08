# Clawker

Clawker is a Go CLI that runs coding agents in Docker containers with controlled access to host resources.

## Shared instructions

Claude Code and Codex must use the same project instructions, rules, skills, and references.
Store instructions in regular `AGENTS.md` files. Each must have a sibling `CLAUDE.md` symbolic link with the relative target `AGENTS.md`.
Before reading or changing files, read every rule in [.agents/rules/](.agents/rules/). A rule without `paths:` applies to every task; `paths:` lists repository-root glob patterns.
Read package `AGENTS.md` files along the path to each file, including files outside the working directory. Requirements for one package tree live in that tree's `AGENTS.md`, not in a rule file.
Use instructions already loaded in the session. Do not load an entire reference directory.
Keep `.agents/` independent of any harness. Store native settings and tool-specific hooks in `.claude/` or `.codex/`. A skill that is also a subagent keeps each harness definition in its `agents/` directory.

## Work rules

- State assumptions. Present different interpretations and ask when the choice is unclear.
- Define a verifiable result before coding. For work with multiple steps, state a short plan and its checks.
- This is an alpha project. Correct design defects and technical debt before continuing affected work.
- Consider architecture, tests, documentation, and effects on users and developers. Use a simpler design when possible.
- Follow the Serena `design` and `architecture` memories; update them when the design changes.
- Write tests before production code. Add missing interfaces, mocks, fakes, and test subpackages as required. All applicable tests must pass.
- Update the README, affected `AGENTS.md` files, documentation, and shared memories after changes.
- When both agents work at the same time, preserve the other agent's edits and check the current diff before writing shared files.
- Use ASD-STE100 Simplified Technical English and its current approved dictionary for chat, documentation, and code comments. Replace unapproved words unless they are standard technical names. Apply the standard; do not restate it.

## Critical rules

- The control plane (CP) runs whenever managed agent containers exist. Only its firewall subsystem is optional. Never gate other CP behavior on `firewall.enable`.
- A CP crash leaves pinned eBPF state without supervision. Before changing CP startup, serving, or their dependencies, read [control-plane safety](controlplane/AGENTS.md#control-plane-safety).
- Do not add lint suppressions without explicit user approval. See [code style](.agents/rules/code-style.md) for errors, constants, and context rules.
- Pin external dependencies to exact versions with integrity verification. See the [dev-checks skill](.agents/skills/dev-checks/SKILL.md).
- When `CLAWKER_AGENT` is set, never run `go test ./...`: the e2e suite tears down the host CP. Use targeted packages or `make test`.

## Commands and references

- Build CLI: `go build -o bin/clawker ./cmd/clawker`
- Unit tests without Docker: `make test`
- Build, integration tests, embeds, hooks, and completion checks: [dev-checks skill](.agents/skills/dev-checks/SKILL.md).
- CLI, config, naming, package boundaries, and terminal behavior: Serena `project-guide` memory.
- Agent file layout and native tool settings: [agent-files skill](.agents/skills/agent-files/SKILL.md).
- Shared skills: `.agents/skills/`. Each `SKILL.md` description names its trigger; invoke the matching one. Behavioral and situational guidance is a skill, not a memory.

## Tools and shared memory

- When Serena is available, initialize it with `initial_instructions`, then `check_onboarding_performed` if available, then `list_memories`. Serena holds project knowledge: `core` is the map; `architecture`, `design`, `key-concepts`, `repo-structure`, `project-guide`, and the domain `*/core` memories hold the rest. Use symbolic tools before full source-file reads. Update shared Serena memories before completing work. If Serena is absent, read the same memories under `.serena/memories/`.
- For GitHub repository documentation, try DeepWiki `ask_question`, then Context7, then other documentation tools. For library APIs, resolve the Context7 library ID before requesting its documentation.
