# Path-specific Copilot instructions

Copilot reads files in this directory that match `*.instructions.md`. This
`README.md` is not one of them; it documents the convention. There are no
instruction files here yet.

## Convention

One file per package tree that carries requirements. The file points at the
package `AGENTS.md` section or the Serena memory; it does not copy the text.

- File name: `<tree>.instructions.md`, for example `controlplane.instructions.md`.
- `applyTo`: the tree's glob. Join several globs with a comma.
- Body: one line that names the section to apply.
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
  the standards that stand in for these files until they exist.
- `.github/skills/code-review/SKILL.md`: the review procedure.
