# Clawker

Clawker is a Go CLI that runs coding agents in Docker containers with controlled access to host resources.

## Shared instructions

Claude Code and Codex must use the same project instructions, skills, and memories.
Store instructions in regular `AGENTS.md` files. Each must have a sibling `CLAUDE.md` symbolic link with the relative target `AGENTS.md`.
Before reading or changing files, read the Serena `core` memory and the memories it names for the touched domain.
Read package `AGENTS.md` files along the path to each file, including files outside the working directory. Requirements for one package tree live in that tree's `AGENTS.md`.
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
- Do not add lint suppressions without explicit user approval. See the Serena `conventions` memory for errors, constants, and context rules.
- Pin external dependencies to exact versions with integrity verification. See the [dev-checks skill](.agents/skills/dev-checks/SKILL.md).
- When `CLAWKER_AGENT` is set, never run `go test ./...`: the e2e suite tears down the host CP. Use targeted packages or `make test`.

## Dependency placement

- Implementation lives in `internal/<package>/`, never in `cmdutil/`. `cmdutil/` holds only the Factory struct, output utilities, and argument validators.
- The only question is who constructs a dependency. Constructed at startup and used by three or more commands: a Factory field. Constructed at startup for fewer commands: the command imports the package and holds it on its Options struct. Needs CLI arguments or runtime context: constructed in the run function, tested by injecting a mock on Options.
- Package ownership: Serena `architecture` memory, Key Packages and Package Import DAG.

## Testing rules

- Docker is always available. Never defer or skip Docker-based tests. When a change touches containers, networks, or volumes, write the integration test in the same task.
- Unit tests are co-located `*_test.go` files without Docker. `test/e2e/` needs Docker; `test/whail/` needs Docker and BuildKit. No build tags; directory separation only. Name tests `TestFunctionName`, `TestFeature_Integration`, or `TestFeature_E2E`.
- Each package in the dependency DAG provides its own test utilities. If a node lacks them, add them first.
- Use unique agent names with random suffixes. Stop containers before removing them. Register cleanup with `t.Cleanup()` and use `context.Background()` inside it. Gate Docker tests with `RequireDocker(t)` or `SkipIfNoDocker(t)`.
- Never discard errors; log cleanup failures with `t.Logf`.
- Co-located `*_test.go` files never import `test/e2e/harness`. Never call `factory.New()` outside `internal/clawkercmd/cmd.go`; build `&cmdutil.Factory{}` literals with test doubles.
- Add no production code only to serve a test seam. Test doubles adapt to production, not the reverse.
- Helpers, fixtures, tiers, and examples: [writing-tests skill](.agents/skills/writing-tests/SKILL.md).

## Commands and references

- Build CLI: `go build -o bin/clawker ./cmd/clawker`
- Unit tests without Docker: `make test`
- Build, integration tests, embeds, hooks, and completion checks: [dev-checks skill](.agents/skills/dev-checks/SKILL.md).
- CLI, config, naming, package boundaries, and terminal behavior: Serena `project-guide` memory.
- Package dependency and layering questions: run `go list -deps ./<pkg>` or `go list -f '{{.ImportPath}} {{.Imports}}' ./...` and answer from that output.
- Agent file layout and native tool settings: [agent-files skill](.agents/skills/agent-files/SKILL.md).
- Shared skills: `.agents/skills/`. Each `SKILL.md` description names its trigger; invoke the matching one. Behavioral and situational guidance is a skill, not a memory.

## Project knowledge: Serena memory bank

Project knowledge lives in the Serena memory bank at `.serena/memories/`: plain Markdown files, one topic per file, with `/` in a name as a topic folder (`cli/core`). Memories reference each other as `` `mem:<name>` ``. Nothing injects memory content; you read what the names and references make relevant.

- Initialize Serena with `initial_instructions`, then `check_onboarding_performed` if available, then `list_memories`. Read `core` first: it is the graph root and names a memory for each work area with what that memory covers. Follow its `mem:` references for the domain you touch. If Serena is absent, read the same files under `.serena/memories/`.
- System knowledge: `architecture` (layers, Factory dependency injection, package DAG, key packages), `design` (philosophy, concepts, security model, lifecycle), `key-concepts` (types and terms), `repo-structure` (directory map), `project-guide` (CLI surface, config schema, design decisions, terminal gotchas), `conventions` (errors, logging, constants, context, output), `tech_stack` (build inputs and pins), and a `<domain>/core` per work area.
- Retained records: `plans/core`, `tracking/core`, `research/core`, `history/core`, `security/core`. Dated; check source before reusing a claim.
- Update the affected memories before completing work. Follow `memory_maintenance` for placement, naming, and links, and run `serena memories check` when the CLI is available. Behavioral and situational guidance is a skill in `.agents/skills/`, not a memory.
- For GitHub repository documentation, try DeepWiki `ask_question`, then Context7, then other documentation tools. For library APIs, resolve the Context7 library ID before requesting its documentation.
