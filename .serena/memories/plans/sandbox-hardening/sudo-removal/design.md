# Item 6: sudo removed from the base image (ruled 2026-10-24)

Part of `mem:plans/sandbox-hardening/initiative` (Task 1).

## Problem

`internal/bundler/assets/Dockerfile.base.tmpl:51` installs `sudo`; the comment at line 30 lists it as a promised tool; the managed agent prompt (`internal/bundler/assets/clawker-agent-prompt.md:213`) tells the agent it may install packages with sudo. No sudoers or group configuration exists in the templates, so the binary is present without a working grant, but a setuid binary in an agent sandbox is surface with no purpose.

## Ruling

Remove it from the platform. A user who adds `sudo` through `build.packages` makes their own policy decision (full grant, out of scope). No deny-list and no validation.

## Removal list

- `internal/bundler/assets/Dockerfile.base.tmpl`: line 51 package; line 30 comment.
- `internal/bundler/dockerfile.go` `isBasePackage` (~615): remove `"sudo"` so a user entry is no longer filtered as a duplicate of the floor and renders an install line.
- `internal/bundler/assets/clawker-agent-prompt.md:213`: "Run shell commands, install packages (with `sudo` if needed)" → no sudo; state that package installation needs the image to provide it (via `build.packages`).
- `clawker-plugin/skills/clawker-support/reference/Dockerfile.base.tmpl`: reference copy, same edits.
- `clawker-plugin/skills/clawker-support/reference/sample-go.yaml:72`: drop `sudo` from the example package list.
- Golden files and embedded assets per `.agents/skills/dev-checks/SKILL.md`.
- Unchanged: `docs/container-internals.mdx`, `docs/threat-model.mdx`, `docs/installation.mdx`, plugin `troubleshooting.md`, `known-issues.md`: these describe host-side rootless helpers.

## Checks

- Bundler test: rendered base Dockerfile contains no `sudo`; `isBasePackage("sudo") == false`; a project with `build.packages: [sudo]` renders an install line.
- Grep listed in the initiative task.
- Runtime: rebuilt image, `which sudo` exits 1 in the agent shell.
