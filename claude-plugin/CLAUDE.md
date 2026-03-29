# Claude Code Plugin Development

All plugins in this directory are Claude Code agent skills for the clawker
project. When working on any plugin here, follow these standards.

## Skill Plugin Conventions

These are Claude Code plugins — not libraries, apps, or scripts. They follow
the Claude Code plugin and skill authoring conventions:

- **Use the Claude Code skill creator** (`/skill-creator` or the skill creator
  agent) for auditing skill definitions, validating SKILL.md frontmatter, and
  checking that skills follow Claude Code's plugin standards.
- **SKILL.md is the skill definition.** Its frontmatter (`name`, `description`,
  `allowed-tools`, `license`, `compatibility`) must conform to the Claude Code
  skill spec. The body is the prompt that runs when the skill is invoked.
- **Reference files are context, not code.** Files in `reference/` directories
  are loaded by skill agents during execution. They teach methodology — they
  are not executed, compiled, or parsed programmatically.
- **plugin.json is the package manifest.** It follows the Claude Code plugin
  spec (`name`, `version`, `author`, `repository`, `homepage`).
- Consult `https://docs.claude.com/en/docs/claude-code/plugins` for the
  current plugin and skill authoring standards when making structural changes.

## Relationship to Clawker Codebase

These plugins live inside the clawker repo but are consumed independently by
Claude Code users. Changes to clawker's config schema, CLI commands, or
architecture may require plugin updates — see each plugin's own CLAUDE.md for
domain-specific guidance.
