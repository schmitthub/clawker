# Path-specific Copilot instructions

Copilot reads files in this directory that match `*.instructions.md`. This
`README.md` is not one of them; it documents the convention. There are no
instruction files here yet.

## Convention

One file per rule in `.claude/rules/`. The file points at the rule; it does
not copy the rule text.

- File name: `<rule>.instructions.md`, where `<rule>` is the base name of
  the file in `.claude/rules/`.
- `applyTo`: copy the globs from the `paths:` front matter of the rule file.
  Join several globs with a comma.
- Body: one line that names the rule file to apply.
- Add `excludeAgent: code-review` when the file is for the Copilot coding
  agent only, or `excludeAgent: cloud-agent` when it is for code review only.

## Template

```markdown
---
applyTo: "internal/git/**"
---

Apply the rules in `.claude/rules/git.md` to these files.
```

## Related files

- `.github/copilot-instructions.md`: repository-wide review priorities and
  the path-to-rule table that stands in for these files until they exist.
- `.github/skills/code-review/SKILL.md`: the review procedure.
