# Clawker Plugin Development

All plugins in this directory are agent-skill collections for the clawker
project, shipped to users' host coding agents (Claude Code, Codex, OpenCode,
Pi). When working on any plugin here, follow these standards.

## Multi-Harness Plugin Shape

A plugin is one canonical set of skills plus thin per-harness manifests. The
skill content is written once; each harness consumes it through its own
install lane:

| Harness | Manifest consumed | Install lane |
|---------|-------------------|--------------|
| Claude Code | `.claude-plugin/plugin.json` | marketplace (`claude plugin install <name>@schmitthub-plugins`) |
| Codex | `.claude-plugin/plugin.json` (read natively as an alternate manifest) | codex marketplace / skills copy |
| OpenCode | none — plain skill dirs | file copy into its skills dir |
| Pi | `package.json` `pi` key (`pi-package` keyword) | `pi install` / file copy |

Rules that keep the content portable to every harness:

- **Skills sit exactly one level under `skills/`** (`skills/<name>/SKILL.md`).
  Claude Code and OpenCode do not discover nested skill directories; never
  introduce grouping subdirectories.
- **SKILL.md frontmatter stays on the common subset** (`name`, `description`,
  `license`, `compatibility`) unless a field is deliberately harness-specific.
  `name` must match the containing directory name.
- **Reference files are context, not code.** Files in `reference/` directories
  are loaded by skill agents during execution. They teach methodology — they
  are not executed, compiled, or parsed programmatically.
- **Manifests never fork the content.** `.claude-plugin/plugin.json` and
  `package.json` both describe the same `skills/` tree; a structural change
  updates every manifest in the same commit.

## Skill Authoring Conventions

- **Use the Claude Code skill creator** (`/skill-creator` or the skill creator
  agent) for auditing skill definitions, validating SKILL.md frontmatter, and
  checking that skills follow the Agent Skills spec.
- **SKILL.md is the skill definition.** The body is the prompt that runs when
  the skill is invoked.
- **plugin.json is the Claude Code package manifest** (`name`, `description`,
  `version`, `author`, `license`, `repository`, `homepage`). Its `version` is
  the plugin's single version of record; bump it once per unmerged release.
- Consult `https://docs.claude.com/en/docs/claude-code/plugins` for current
  Claude Code plugin standards when making structural changes.

## Relationship to Clawker Codebase

These plugins live inside the clawker repo solely to prevent drift: changes to
clawker's config schema, CLI commands, or architecture land in the same tree
as the skill docs they invalidate. Distribution is external — the
`schmitthub/claude-plugins` marketplace repo pins this repo + subdir at a
released SHA (auto-bumped by CI on merges to main that touch this directory).
See each plugin's own CLAUDE.md for domain-specific guidance.
