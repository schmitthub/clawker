# Master Dockerfile.tmpl Refactor Draft (Phase 1 blueprint)

Line-by-line disposition of `internal/bundler/assets/Dockerfile.tmpl` (665
lines, read at HEAD 2026-07-03): clawker infra stays in master; claude-code
content moves to `harnesses/claude_code/Dockerfile.harness.tmpl` block defines;
master grows `{{block}}` declarations with empty defaults at the exact
positions.

## First-principles strata (the actual frame — added after user correction)

Test per line: "swap the harness — does it stay?"

**Stratum 1 — clawker contract** (supervision/security/host-integration; identical for every harness):
clawkerd PID-1 chain (builder stages, COPY clawkerd, ENTRYPOINT, HEALTHCHECK on
/var/run/clawker/ready), runtime dirs (/run/clawker root-0700 bootstrap,
/var/log/clawker, /var/run/clawker), firewall MITM CA + cert-trust env
(SSL_CERT_FILE/CURL_CA_BUNDLE/NODE_USE_SYSTEM_CA), host integration (host-open +
BROWSER, git-credential-clawker, callback-forwarder, clawker-socket-server,
.gnupg/.ssh pre-creates, .clawker-share), user identity (host UID/GID, docker
group, CLAWKER_USER), workspace + WORKDIR, command-history dir.

**Stratum 2 — base environment** (clawker's DX floor + user/project config hooks;
harness-agnostic by policy): packages, zsh/zsh-in-docker, fzf, node+nvm,
git-delta, gh, locale/TERM, .local/bin PATH (XDG), Instructions.* + Inject.* renders.

**Stratum 3 — harness** (vanishes on harness swap): install + version ARG, tool
env (config-dir, telemetry), seeds, managed/enterprise files, instruction-file
delivery, CMD, config-dir pre-create ({{.HarnessConfigDir}} param).

Nuance: clawker-agent-prompt.md CONTENT is stratum 1 (clawker's container guide);
only its DESTINATION (/etc/claude-code/CLAUDE.md) is stratum 3. Block copies a
clawker-supplied file ({{.AgentPromptSrc}}-style param) to a harness-known path.

Block positions = the five capabilities stratum 3 needs from the master:
| position | capability offered to harness author |
|---|---|
| BLOCK_1 | root + base tooling, before user context: write /etc, system-wide files build-time tool sessions must see |
| BLOCK_2 | user context + base env active: declare the tool's runtime environment |
| BLOCK_3 | everything ready, end of volatile zone: install tool + stage artifacts (cache-cheapest for weekly churn) |
| BLOCK_4 | root regained, before clawker asset train: root-owned placements |
| BLOCK_5 | after ENTRYPOINT: the command clawkerd supervises |

## Disposition table

| lines | section | verdict |
|---|---|---|
| 1-2 | header comment (`Claude Code {{.ClaudeVersion}}`) | MASTER — reword to harness-neutral params ({{.HarnessName}}/{{.HarnessVersion}}) |
| 4-24 | builder stages (callback-forwarder, socket-server) | MASTER |
| 26-37 | FROM final, TZ, NODE_USE_SYSTEM_CA | MASTER (comment mentions CC hooks — reword) |
| 39-51 | user ARGs + after_from inject | MASTER |
| 53-143 | system packages + after_packages inject | MASTER |
| 145-169 | Docker CLI | MASTER |
| 171-308 | root Node.js install | MASTER (post-#408 baked for all; reword CC-hook comment) |
| 310-322 | locale + root_run | MASTER |
| 324-354 | user setup, docker group, after_user_setup | MASTER |
| 356-369 | workspace/runtime dirs mkdir/chown — **`.claude` hardcoded in list** | MASTER, parameterized: `/home/${USERNAME}/{{.HarnessConfigDir}}` — value from harness.yaml `staging.config_dir.path` (single source shared with create-time volume mount target). Empty → omitted |
| 371-405 | bash history, WORKDIR, git-delta | MASTER |
| 408-430 | **managed-settings.json heredoc** | → `harness_managed_config` block (early root; position fixed — build-time `claude` in inject points reads it) |
| 432-448 | USER switch, CLAWKER_USER, HOME/PATH `.local/bin` | MASTER (PATH is generic XDG; reword claude-flavored comment) |
| 451-461 | BROWSER, TERM/COLORTERM/LANG | MASTER |
| 453-456 | **ENV CLAUDE_CONFIG_DIR** | → `harness_env` block |
| 462-492 | **telemetry env: CLAUDE_CODE_* gates + full OTEL_* set (incl. {{.Otel*}} params/conditionals)** | → `harness_env` block |
| 494-514 | nvm, ENV SHELL, after_user_switch | MASTER |
| 517-555 | zsh-in-docker, user_run | MASTER |
| 557-578 | **ARG CLAUDE_CODE_VERSION (declaration-adjacency comment!) + install RUN (BuildKit npm cache-mount variant)** | → `harness_install` block; ARG renamed `HARNESS_VERSION={{.HarnessVersion}}` |
| 580-586 | **seed COPYs → `~/.claude-init/`** | → `harness_install` block (same block, per decided shape) |
| 588-593 | Instructions.Copy | MASTER |
| 595-600 | after_claude_install inject | MASTER mechanism, RENAMED `after_harness_install`; sits immediately after `harness_install` block |
| 602-611 | HEALTHCHECK, before_entrypoint | MASTER |
| 613-631 | USER root + late-block header comment | MASTER (comment rewrite: references claude seeds/paths) |
| 632 | **COPY clawker-agent-prompt.md → /etc/claude-code/CLAUDE.md** | → `harness_root_assets` block |
| 633-660 | firewall CA, host-proxy binaries, clawkerd | MASTER |
| 663 | ENTRYPOINT clawkerd | MASTER |
| 664 | **CMD ["claude"]** | → `harness_cmd` block |

## Resulting master block declarations (5 + 1 param)

**BLOCK NAMES: TBD BY USER** — naming attempts (content-prescriptive, then
scope+order, then master-internal events) all rejected; user will name the five
positions himself. Placeholders BLOCK_1..5 below; positions are final, names are not.

| placeholder | position (final) |
|---|---|
| BLOCK_1_TBD | root scope, after base tooling (git-delta), before `USER ${USERNAME}` (~408) |
| BLOCK_2_TBD | user scope, after the master's static-env section (~453) |
| BLOCK_3_TBD | user scope, after user_run instruction renders — version-ARG cache zone (~557) |
| BLOCK_4_TBD | root scope, after trailing `USER root`, before clawker asset COPYs (~632) |
| BLOCK_5_TBD | final instruction — CMD position (664) |

Constraint that stands regardless of names: block namespace disjoint from
project-config inject keys (after_from, after_packages, after_user_setup,
after_user_switch, after_harness_install, before_entrypoint); loader enforces.

**Naming is EVENT-CENTRIC, positional opportunities, no `harness_` prefix**
(user decisions 2026-07-03): each block name explains what Dockerfile build
event precedes/follows it — never named after expected content. Any harness
uses any position for whatever benefits from it.

```
...git-delta...
{{block "BLOCK_1_TBD" .}}{{end}}   # root scope, before USER ${USERNAME} (~line 408)
USER ${USERNAME} ...
ENV BROWSER=... TERM=... LANG=...
{{block "BLOCK_2_TBD" .}}{{end}}       # after static-env section (~line 453)
...nvm, zsh-in-docker, user_run...
{{block "BLOCK_3_TBD" .}}{{end}}       # after user-mode RUNs; ARG-adjacent cache zone (~line 557)
{{/* Instructions.Copy */}}
{{/* inject: after_harness_install */}}
...HEALTHCHECK, before_entrypoint, USER root, late-block header...
{{block "BLOCK_4_TBD" .}}{{end}}    # after trailing USER root, before clawker assets (~line 632)
...firewall CA, binaries, clawkerd...
ENTRYPOINT ["/usr/local/bin/clawkerd"]
{{block "BLOCK_5_TBD" .}}{{end}}                  # final instruction (line 664)
```

Claude fills all 5; codex fills 3 (BLOCK_2_TBD, BLOCK_3_TBD, cmd);
minimal harness fills 1 (cmd).

RULE: block names ∪ project-config inject-key names stay DISJOINT forever
(after_from, after_packages, after_user_setup, after_user_switch,
after_harness_install, before_entrypoint are reserved); loader validates a
harness tmpl defines only declared block names. The block at ~453 is named
`BLOCK_2_TBD` (not `after_user_switch`) for exactly this reason.

Plus `{{.HarnessConfigDir}}` param in the mkdir/chown RUN (line ~367),
sourced from harness.yaml `staging.config_dir.path`.

## Special cases / gotchas

1. **ARG cache adjacency survives**: the `ARG HARNESS_VERSION` declaration
   stays inside `harness_install`, rendered at the same template position —
   BuildKit declaration-line invalidation semantics unchanged. The long
   explanatory comment moves INTO the claude block verbatim.
2. **`/etc/claude-code` creation dependency** is internal to the harness:
   `harness_managed_config` RUN creates it; `harness_root_assets` COPY
   relies on it (COPY auto-creates intermediates anyway). No master concern.
3. **Seed dest `~/.claude-init/` stays verbatim in Phase 1** (byte-identical
   gate). Generalization to `~/.clawker/seed/` + generic apply script lands
   in 3e and deliberately regenerates goldens then.
4. **Byte-identical gate vs comment rewording**: master comments mentioning
   claude (header, NODE_USE_SYSTEM_CA, .local/bin, late-block header) — any
   reword changes rendered bytes. Policy: Phase 1 keeps ALL bytes identical
   (blocks emit current content verbatim, master comments untouched);
   comment neutralization = separate commit with golden regen whose diff is
   comments-only (human-reviewable).
5. **DockerfileContext renames** (3c): `ClaudeVersion`→`HarnessVersion`;
   Otel* params stay (consumed by claude's harness_env block); context gains
   `HarnessConfigDir`, `HarnessName`.
6. **InjectConfig**: `after_claude_install` key → `after_harness_install`
   with deprecation shim (3a).
7. **Test invariants to migrate** (`TestBuildContext_LateClawkerBlock`,
   `TestBuildContext_CollapsedChmod`, dockerfile_test.go seed assertions):
   rewrite against block-composed output; same positional invariants
   (managed config before first USER switch; seeds before trailing USER
   root; prompt/CA/binaries/clawkerd after; clawkerd last before ENTRYPOINT).

## What claude_code/Dockerfile.harness.tmpl contains (verbatim lift)

- `{{define "BLOCK_1_TBD"}}` — lines 408-430 (managed-settings mkdir + heredoc + comment)
- `{{define "BLOCK_2_TBD"}}` — lines 453-456 + 462-492 (CLAUDE_CONFIG_DIR + gates + OTEL set w/ {{.Otel*}} conditionals)
- `{{define "BLOCK_3_TBD"}}` — lines 557-586 (ARG comment + ARG + install RUN both BuildKit variants + seed COPY trio)
- `{{define "BLOCK_4_TBD"}}` — line 632 (prompt COPY + its comment)
- `{{define "BLOCK_5_TBD"}}` — line 664
