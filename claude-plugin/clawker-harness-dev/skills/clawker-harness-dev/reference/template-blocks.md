# Dockerfile.harness.tmpl — Block Slot Reference

A harness template is a set of `{{define "block_N"}}...{{end}}` bodies. The
master harness-image template declares six empty slots and owns instruction
ordering and cache architecture; composition validates your fragment first —
defining any name that is not a declared block is a build error.

- **Declared blocks:** `block_1` … `block_6` (names are positional
  opportunities, never content-prescriptive; define any subset).
- **Reserved names** (never definable by a bundle): `Dockerfile` (the
  master's own name) and the project-config inject-point keys `after_from`,
  `after_packages`, `after_user_setup`, `after_user_switch`,
  `after_claude_install`, `after_harness_install`, `before_entrypoint`.
  Errors: `defines reserved name ...` / `defines unknown block ...;
  declared blocks: [...]`.

## Render positions

The harness image builds `FROM` the per-project shared base (user already
created, `~/.local/bin` already on `ENV PATH`, zsh configured). The final
stage renders, in order:

| Position | USER | SHELL | Notes |
|---|---|---|---|
| `SHELL ["/bin/sh"]`, `ARG USERNAME`, `USER root` | — | — | ARGs do not survive FROM; the master re-declares `USERNAME` here and `ZSH_ENV` later. SHELL *does* carry over via image config, so the master resets it to sh. |
| toolchain **root** fragments | root | sh | Harness-declared toolchains not already in the base — rendered before block_1 so every block can rely on them. |
| **block_1** | root | sh | First root step of the harness image. Heavy root-scope installs. |
| **block_2** | root | sh | Late root scope before the user switch. The claude bundle writes `/etc/claude-code/managed-settings.json` here — root-owned config that must exist before any build-time harness invocation. |
| volume-dir `mkdir -p` + `chown` | root | sh | Auto-generated from the manifest `volumes:` list. |
| `USER ${USERNAME}`, `ARG ZSH_ENV=/home/${USERNAME}/.zshenv` | — | — | `ZSH_ENV` is declared above the user-scope anchors because ARGs scope lexically downward and fragments may reference it (e.g. nvm's `PROFILE=${ZSH_ENV}`). |
| toolchain **user** fragments | user | sh | Harness-declared, not already in base. |
| **block_3** | user | sh | Static env. `ENV` your harness's config-dir variable here, pointing at the declared volume path (codex: `ENV CODEX_HOME=/home/${USERNAME}/.codex`). |
| `SHELL ["/bin/zsh", "-o", "pipefail", "-c"]` | — | — | Blocks 4–6 run under zsh. |
| **block_4** | user | zsh | The harness install. `{{.HarnessVersion}}` is available (as in every block). See the ARG cache rule below. |
| seed staging + seed-manifest | user | zsh | Auto-generated from manifest `seeds:` — COPYs to `~/.clawker/seed/` + heredoc manifest. Deliberately in user scope so `after_harness_install` / `before_entrypoint` inject points can reference staged seeds. |
| project inject points (`after_harness_install`, `before_entrypoint`) | user | zsh | Project-config content, not yours — but your block_2/PATH decisions determine whether a user's build-time harness invocation works here. |
| `USER root` | — | — | Final image stays at USER root: clawkerd is PID 1 as root and drops privileges when spawning the CMD. |
| **block_5** | root | zsh | Late root assets that roll with release cadence (the claude bundle COPYs its managed agent prompt to `/etc/claude-code/CLAUDE.md` here). Sits at the head of the clawker-managed late block (firewall CA, host-proxy binaries, clawkerd) so bumps invalidate only this tail. |
| `ENTRYPOINT ["/usr/local/bin/clawkerd"]` | — | — | Fixed; a bundle cannot change PID 1. |
| **block_6** | — | — | The final instruction — CMD position. `CMD ["<your-cli>"]`. |

## Template data available in blocks

Blocks render against the same context as the master. The fields a bundle
legitimately uses:

- `{{.HarnessVersion}}` — resolved version string (or `latest` on
  resolver=none/failure).
- `{{.BuildKitEnabled}}` — gate `RUN --mount=type=cache,...` directives
  (the legacy builder rejects them).
- `${USERNAME}` — Dockerfile ARG (not a template field) for the
  unprivileged container user; use it in paths (`/home/${USERNAME}/...`).
- `${ZSH_ENV}` — ARG pointing at the user's `.zshenv`, sourced by zsh on
  every invocation (the append-target for shell init lines).

## Critical: the PATH gotcha

clawkerd spawns the CMD via **direct exec — no login shell, no rc files,
no nvm/rustup shell hooks**. The process environment is the image's static
`ENV`. Therefore the harness CLI binary MUST land on the image `ENV PATH`:

- `~/.local/bin` — on PATH via the base image's
  `ENV PATH=${HOME}/.local/bin:$PATH`; where both shipped installers put
  their entry point. Preferred for user-scope installs.
- `/usr/local/bin` — for root-scope installs.

An install that only becomes visible through shell init (an
`nvm use`-prefixed npm global, a rustup env sourced in `.zshenv`) works in
interactive shells and `RUN` steps but is **invisible to the CMD spawn** —
the container starts and immediately fails with exec not found.

## Critical: the version-ARG cache rule (block_4)

Declare the version ARG **directly above its only consumer**, inside
block_4 — never hoisted to the top of the stage:

```dockerfile
{{define "block_4" -}}
ARG MYCLI_VERSION={{.HarnessVersion}}
RUN curl -fsSL https://example.com/install.sh | sh -s ${MYCLI_VERSION}
{{- end}}
```

Under BuildKit (Docker 23+ default) a changed ARG default busts the cache
at the ARG's **declaration line**, not at first use. A harness release rolls
the rendered default; adjacent placement scopes the invalidation to the
install layer + everything below it, leaving toolchain fragments and blocks
1–3 cached. Three properties of ARG (vs ENV): declaration-line cache
scoping, build-only (absent from the running container), and user override
via `clawker build --build-arg MYCLI_VERSION=<v>`.

## Volume shadowing at install time

Declared volumes mount over their image paths at runtime, shadowing baked
content. If your installer wants to write its payload into the same dir you
declared as the config volume, redirect the install-time location. The codex
bundle is the worked example: runtime `CODEX_HOME` (block_3) points at the
`.codex` volume, but the installer runs with
`CODEX_HOME=/home/${USERNAME}/.local/share/codex-install` so the payload
lives outside the volume and the `~/.local/bin/codex` symlink never
dangles.

## Worked example: the codex fragment (complete)

Three blocks is a complete harness template:

```dockerfile
{{define "block_3" -}}
# Tell codex where its config lives — must match the config volume mount.
ENV CODEX_HOME=/home/${USERNAME}/.codex
{{- end}}

{{define "block_4" -}}
ARG CODEX_VERSION={{.HarnessVersion}}
# Canonical standalone installer: sha256-verified GitHub release binary,
# command lands in ~/.local/bin (already on the image ENV PATH). Payload
# installed OUTSIDE the .codex volume path (see volume shadowing above).
RUN curl -fsSL https://chatgpt.com/codex/install.sh | \
      CODEX_NON_INTERACTIVE=1 CODEX_RELEASE=${CODEX_VERSION} \
      CODEX_HOME=/home/${USERNAME}/.local/share/codex-install sh
{{- end}}

{{define "block_6" -}}
CMD ["codex"]
{{- end}}
```

## Claude fragment highlights (the full-featured example)

Read the materialized claude bundle for the complete text; what it
demonstrates beyond codex:

- **block_2 (root, pre-user-switch):** writes
  `/etc/claude-code/managed-settings.json` injecting
  `PATH=/home/${USERNAME}/.local/bin:${PATH}` — Claude Code's Bash tool
  sources its own shell snapshot after zsh init, so the managed-settings
  mechanism is the only reliable PATH injection for it. Root-owned config
  that build-time inject points read belongs in early root scope, not
  block_5.
- **block_3:** the config-dir ENV (`CLAUDE_CONFIG_DIR`) plus the OTEL
  telemetry env block — `CLAUDE_CODE_ENABLE_TELEMETRY`, exporter/protocol
  vars, and template-conditional fields (`{{.OtelEndpoint}}`,
  `{{if .OtelIncludeAccountUUID}}` etc.) wired from the monitoring config.
  Harness-specific telemetry env belongs to the bundle, not to clawker.
- **block_4:** the ARG-adjacency pattern above, plus a
  `{{if .BuildKitEnabled}}` cache-mount variant of the install RUN.
- **block_5:** COPY of `assets/clawker-agent-prompt.md` to a root-owned
  `/etc` path — release-cadence assets at the tail.
