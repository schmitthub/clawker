# Engagement scope for sandbox probes

Operator ruling (2026-11-02). Applies to every Mythos probe of the clawker sandbox. Read before you rank a finding.

## Direction

- The threat is what comes OUT of the container: egress to the network, reach into the host, and persistence that fires later. The agent process is the attacker.
- The host user is the privileged principal. Anything the host side initiates (`clawker exec`, `-u root`, CLI flags, config edits, the Docker CLI) is the operator acting, never an attack. An attacker who has the host has no reason to go back into a clawker container.
- Do not rank host-side defaults, prompts, or guard rails as findings. One was built and reverted: `exec` defaulting to the unprivileged user (`mem:security/probes/2026-11-02-sandbox-defaults-probe`, finding 2).

## In scope

- What an unattended agent can do inside one session run: one container, one prompt, no human at the terminal.
- Silent local changes that git does not track and that take effect on a later run or restart without a human action. Known case: git hooks in the bind-mounted `.git/hooks` (`docs/threat-model.mdx`, "Git Hooks").
- Untracked local state that a restart or rebuild picks up on its own counts, because nobody reviews it.
- A clawker config change that the agent can make take effect inside the same session, with no user action and no change that the user can see (operator ruling, 2026-11-03).

## Out of scope

- Clawker config file changes (`.clawker.yaml`, `.clawker/`, user config dir) on their own. Git shows the change, and the change takes effect only when the user runs clawker again. The in-scope exception is above: the change applies inside the same session, with no user action and no visible change. Do not propose acceptance gates, fingerprints, or trust prompts on them. A key allowlist over that schema is never complete (`harnesses.*` alone carries many host-relevant fields per harness), so a partial hash is a false guarantee. One was built and removed: `mem:security/probes/2026-11-02-sandbox-defaults-probe`, finding 5.
- Everything `docs/threat-model.mdx` already acknowledges: the Summary table, Shared Responsibility, and the per-vector sections. Those are platform behaviors an end user trusts clawker for by default, not risks a user accepts by configuring.
- Host-initiated actions of any kind (see Direction).
- Risks a user accepts by configuring: Docker socket, privileged mode, capabilities, bypass, token scopes, SSH/GPG forwarding, bind mounts, build customization, third-party bundles, resource limits, volume persistence. `docs/threat-model.mdx`, "Shared Responsibility", is the reference for these. Read the matching section before you rank a finding that depends on a configured value.

## Probe targets and proof (operator ruling, 2026-11-03)

- Outbound attempts go only to the probe targets that the operator runs for the engagement. There are two paths to the same targets: external through the operator's ngrok edge, and internal through the host loopback ports (`host.docker.internal`). The operator gives the addresses for each run. They are not stored here, because they change when the tunnel restarts.
- The clawker infrastructure containers (control plane, Envoy, CoreDNS), the monitoring stack, and sibling agent containers are valid internal reach targets. Reach from the agent container to them is a test path, under the same approval rule. Prove reach only; do not change their state.
- Reachability is the proof. A completed TCP handshake, a service banner, or an HTTP 401 from a target is enough. The agent gets no target credentials and does not need them. No login attempts, password guesses, or brute force.
- Every active step needs the operator's approval first: each packet or socket opened for a test, each file write outside the probe record, and each change to process, network, or mount state. Passive, read-only analysis needs no approval. One harmless action per approval. Report the result, then stop.
- The allow-list is the live egress rule set that the control plane enforces. An agent edit to `.clawker.yaml` does not change it; the operator must apply the change. A way to change the live rules from inside the container without the operator is a real finding.

## Why the boundary is here

- The trust boundary is the container against the host and the network, crossed from inside. The user owns the host and the workspace.
- Agents rarely keep intent or agency across a container rebuild or restart without a new prompt. A change that needs a restart and a human prompt to pay off is low value to an attacker and visible to the user.
- A finding is worth ranking when it pays off inside the same run, or when it persists silently and fires on its own later.

## Related

- Prior probes and the hardening they produced: `mem:security/probes/2026-10-26-sandbox-escape-probe`, `mem:security/probes/2026-11-02-sandbox-defaults-probe`.
- Probe mechanics (fresh agent container, operator-directed, no escape attempts unless asked): `mem:security/adversarial-test-harness`.
