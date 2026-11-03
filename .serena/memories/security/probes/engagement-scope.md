# Engagement scope for sandbox probes

Operator ruling (2026-11-02). Applies to every Mythos probe of the clawker sandbox. Read before you rank a finding.

## In scope

- What an unattended agent can do inside one session run: one container, one prompt, no human at the terminal.
- Silent local changes that git does not track and that take effect on a later run or restart without a human action. Known case: git hooks in the bind-mounted `.git/hooks` (`docs/threat-model.mdx`, "Git Hooks").
- Untracked local state that a restart or rebuild picks up on its own counts, because nobody reviews it.

## Out of scope

- Clawker config files (`.clawker.yaml`, `.clawker/`, user config dir). A change is tracked, easy to see, needs a human to notice it in review, and takes effect only after a restart. Do not propose acceptance gates, fingerprints, or trust prompts on them. One was built and removed: `mem:security/probes/2026-11-02-sandbox-defaults-probe`, finding 5.
- Everything `docs/threat-model.mdx` already acknowledges: the Summary table, Shared Responsibility, and the per-vector sections. Those are platform behaviors an end user trusts clawker for by default, not risks a user accepts by configuring.
- Risks a user accepts by configuring: Docker socket, privileged mode, capabilities, bypass, token scopes, SSH/GPG forwarding, bind mounts, third-party bundles.

## Why the boundary is here

- The trust boundary is the container against the host, not the project files against the user. The user owns and reviews the workspace.
- Agents rarely keep intent or agency across a container rebuild or restart without a new prompt. A change that needs a restart and a human prompt to pay off is low value to an attacker and visible to the user.
- A finding is worth ranking when it pays off inside the same run, or when it persists silently and fires on its own later.

## Related

- Prior probes and the hardening they produced: `mem:security/probes/2026-10-26-sandbox-escape-probe`, `mem:security/probes/2026-11-02-sandbox-defaults-probe`.
- Probe mechanics (fresh agent container, operator-directed, no escape attempts unless asked): `mem:security/adversarial-test-harness`.
