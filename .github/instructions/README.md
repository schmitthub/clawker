# Path-specific Copilot instructions

Copilot reads files in this directory that match `*.instructions.md`. This
`README.md` is not one of them; it documents the convention. There are no
instruction files here yet.

## Convention

One file per rule in `.agents/rules/`. The file points at the rule; it does
not copy the rule text.

- File name: `<rule>.instructions.md`, where `<rule>` is the base name of
  the file in `.agents/rules/`.
- `applyTo`: copy the globs from the `paths:` front matter of the rule file.
  Join several globs with a comma.
- Body: one line that names the rule file to apply.
- `excludeAgent`: optional. GitHub accepts `"code-review"` or
  `"cloud-agent"`. With `excludeAgent: "code-review"`, only Copilot cloud
  agent reads the file. With `excludeAgent: "cloud-agent"`, only Copilot code
  review reads the file. Without the key, both read it. Source:
  [Adding repository custom instructions for GitHub Copilot](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions).

## Template

```markdown
---
applyTo: "controlplane/**"
---

Apply the Control-plane safety section of `controlplane/AGENTS.md` to these files.
```

## Related files

- `.github/copilot-instructions.md`: repository-wide review priorities and
  the shared rule directory that stands in for these files until they exist.
- `.github/skills/code-review/SKILL.md`: the review procedure.
