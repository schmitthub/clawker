# OpenClaw harness for clawker — recon facts (2026-07-28)

Ground truth gathered before planning. Facts only; design decisions live in
`initiative_openclaw_harness/plan`.

## Sources

- Repo `/home/clawker/.clawker/openclaw-deploy` (added as session working dir;
  branch renamed `feat/openclaw-harness`). Pulumi TS IaC, precursor to clawker.
- https://docs.openclaw.ai — `start/wizard-cli-automation`, `start/wizard-cli-reference`,
  `start/onboarding-overview`, `install/docker`. Domain IS firewall-allowed here.
- clawker harness contract: `internal/config/harness_schema.go`,
  `internal/bundle/assets/harnesses/{claude,codex}`, `clawker-test-bundle/`.

## OpenClaw runtime shape (upstream)

- Node app, npm-distributed (`npm i -g openclaw`), CLI symlinked from
  `$(npm root -g)/openclaw/dist/entry.js`. Official images:
  `ghcr.io/openclaw/openclaw:latest`, `openclaw/openclaw:latest`.
- **Daemon, not an interactive CLI.** `openclaw gateway run --port 18789`.
  Control UI + API on 18789; bridge port 18790. Health: `/healthz`, `/readyz`
  (unauthenticated). `gateway.bind` = `loopback` | `lan`.
- State dirs (all under `$OPENCLAW_CONFIG_DIR`, default `~/.openclaw`):
  - `openclaw.json` — behavior config
  - `agents/<agentId>/agent/auth-profiles.json` — provider creds
  - `agents/<agentId>/agent/openclaw-agent.sqlite` + shared state sqlite
  - `credentials/whatsapp/<accountId>/`, `tools/signal-cli/<version>/`
  - `.env` (holds `OPENCLAW_GATEWAY_TOKEN`), `identity/`, `sessions/`
  - workspace: `$OPENCLAW_WORKSPACE_DIR`, default `~/.openclaw/workspace`
  - auth profile secrets: CORRECTED 2026-08-01 — `~/.config/openclaw` is ONLY the
    legacy OAuth sidecar compat path (legacy-oauth-sidecar.ts, read-only, "until
    doctor migrates back to inline auth-profiles.json"); fresh installs store OAuth
    inline at `~/.openclaw/agents/<id>/agent/auth-profiles.json`. Upstream compose
    mounts host `$OPENCLAW_AUTH_PROFILE_SECRET_DIR` (default
    `~/.openclaw-auth-profile-secrets`) at `/home/node/.config/openclaw` purely for
    that migration support. The harness `secrets` volume was removed (bundle PR #2)
    — a from-scratch deployment never writes the dir.
  - rolling logs `/tmp/openclaw/`; plugin package roots
- Env: `OPENCLAW_IMAGE`, `OPENCLAW_HOME_VOLUME`, `OPENCLAW_SANDBOX`,
  `OPENCLAW_DOCKER_SOCKET`, `OPENCLAW_SKIP_ONBOARDING`, `OPENCLAW_EXTRA_MOUNTS`,
  `OPENCLAW_PREFER_PNPM`, `OPENCLAW_NO_RESPAWN`, `OPENCLAW_GATEWAY_{PORT,BIND,TOKEN}`.
- Channels are plugins: Discord, Telegram, Slack, WhatsApp, Signal, Matrix,
  Teams, iMessage, Google Chat, Zalo.
- **OpenClaw ships its own sandbox** (`agents.defaults.sandbox`, `OPENCLAW_SANDBOX`,
  `scripts/sandbox-setup.sh`) that spawns Docker containers → needs docker socket.

## Non-interactive onboarding (verified against live docs)

```
openclaw onboard --non-interactive --accept-risk \
  --mode local --auth-choice apiKey --anthropic-api-key "$KEY" \
  --secret-input-mode plaintext|ref \
  --gateway-bind loopback --gateway-port 18789 \
  --install-daemon|--no-install-daemon --daemon-runtime node \
  --skip-bootstrap --skip-skills [--skip-channels --skip-daemon --skip-health]
```
- `--accept-risk` MANDATORY with `--non-interactive`. `--json` alone does NOT
  imply non-interactive.
- `--secret-input-mode ref` stores `{source:"env",provider:"default",id:"<VAR>"}`
  instead of plaintext — the right mode for a git-tracked state dir.
- Per-provider flags: `--auth-choice` ∈ apiKey, custom-api-key, openai-api-key,
  gemini-api-key, mistral-api-key, moonshot-api-key, ollama, opencode-zen,
  opencode-go, synthetic-api-key, ai-gateway-api-key, zai-api-key,
  cloudflare-ai-gateway-api-key, openrouter (used by openclaw-deploy).
- Post-onboard config via `openclaw config set <key> <json>`. Keys seen in
  openclaw-deploy `reference/setup.sh`: `gateway.controlUi.allowedOrigins`,
  `gateway.auth.allowTailscale`, `gateway.controlUi.dangerouslyDisableDeviceAuth`,
  `gateway.trustedProxies`, `tools.profile`, `browser.headless`,
  `browser.noSandbox`, `skills.install.nodeManager`.
- Extra agents: `openclaw agents add <name> --workspace ... --non-interactive --json`
  (`main` reserved).
- Headless OAuth is NOT scriptable — do it on a desktop, copy
  `agents/<id>/agent/auth-profiles.json` to the host.

## openclaw-deploy (precursor architecture)

Pulumi TS → VPS (Hetzner tested / DigitalOcean / OCI) running a compose-ish
topology that clawker later generalized:

| openclaw-deploy piece | clawker equivalent today |
|---|---|
| Envoy sidecar, SNI/TCP allowlist (`templates/envoy.ts`) | CP-managed Envoy + path/proto rules |
| CoreDNS allowlist, NXDOMAIN default (`templates/coredns.ts`) | CP-managed custom CoreDNS |
| `firewall-bypass` ssh one-liner, 30s auto-close | `clawker firewall bypass <dur> --agent` |
| iptables REDIRECT in tailscale sidecar netns | eBPF cgroup redirect |
| `templates/agent-prompt.ts` injected env briefing | clawker managed prompt (`/etc/claude-code/CLAUDE.md` analog) |
| `config/domains.ts` hardcoded allowlist | harness `egress:` floor + project `security.firewall` |
| `ocm` management CLI (`scripts/manage.sh`) | `clawker container/firewall/...` |
| Tailscale sidecar + Serve (TLS, remote access) | **no clawker equivalent** — stays IaC-only |

`config/domains.ts` allowlist (reusable as egress-floor seed):
`clawhub.com`, `registry.npmjs.org`, `api.search.brave.com`,
`api.anthropic.com`, `api.openai.com`, `generativelanguage.googleapis.com`,
`openrouter.ai`, `api.x.ai`, `github.com`, `*.githubusercontent.com`, `ghcr.io`,
`formulae.brew.sh`, `*.tailscale.com`, `*.api.letsencrypt.org`.
Note `clawhub.com` = OpenClaw plugin/skill registry.

Repo layout: `index.ts`, `components/` (bootstrap, envoy, envoy-proxy,
gateway, gateway-image, gateway-init, gateway-post-init, oci-infra, server,
tailscale-sidecar), `config/` (defaults, digests, domains, types),
`templates/` (agent-prompt, bypass, coredns, dockerfile, entrypoint, envoy,
serve, sidecar), `reference/` (generated Dockerfile, docker-compose.yml,
entrypoint.sh, setup.sh, Corefile, envoy.yaml, serve-config.json),
`scripts/` (manage.sh, update-base-digests.sh), `tests/` (vitest).

## Upstream Dockerfile — read verbatim 2026-07-28 (SUPERSEDES openclaw-deploy's)

Source: `raw.githubusercontent.com/openclaw/openclaw/main/Dockerfile`, 386 lines.
(Required a firewall fix first — see the regex-bug note at the end of this memo.)
openclaw-deploy's `reference/Dockerfile` is a GENERATED artifact of that repo and
is now stale; upstream is the authority.

- **Multi-stage build FROM SOURCE**, not `npm i -g openclaw`. Stages:
  workspace-deps → bun-binary → build → runtime-assets → base-runtime → runtime.
- Runtime base `node:24-bookworm-slim`, pinned by digest. Build stages use full
  bookworm. `ENTRYPOINT ["tini","-s","--"]`, `CMD ["node","openclaw.mjs","gateway"]`.
- `/usr/local/bin/openclaw` → symlink to `/app/openclaw.mjs`.
  **Command form is `openclaw gateway`** — openclaw-deploy's
  `openclaw gateway run --port 18789` may be stale. Verify in P1.
- Runtime apt deps (the whole list): `ca-certificates curl git hostname lsof
  openssl procps python3 tini`. **No libsecret-tools. No homebrew.**
- pnpm via corepack, `COREPACK_HOME=/usr/local/share/corepack` (shared so the
  non-root user needs no first-run network fetch).
- Built-in `HEALTHCHECK` hitting `/healthz` every 3m. Aliases `/health`, `/ready`.
- Runs as `USER node` (uid 1000). `NODE_ENV=production`.

### Bridge-network bind — DECIDES the access question (lines 374-377, verbatim)

```
# IMPORTANT: With Docker bridge networking (-p 18789:18789), loopback bind
# makes the gateway unreachable from the host. Either:
#   - Use --network host, OR
#   - Override --bind to "lan" (0.0.0.0) and set auth credentials
```

clawker containers are on clawker-net = bridge. `--network host` is NOT an
option: it removes the cgroup the eBPF redirect attaches to, i.e. no firewall.
→ **`gateway.bind: lan` + gateway token is MANDATORY**, a harness default, not
a setup-script step.

### Volume mount points pre-created by upstream (lines 354-362)

Matches clawker's volume model exactly — "so first-run Docker volumes copy node
ownership from the image instead of starting as root-owned" (issue #85968:
`/home/node/.config` must be created node-owned FIRST so the leaf inherits):

| path | mode | owner |
|---|---|---|
| `/home/node/.config` | 0755 | node:node |
| `/home/node/.openclaw` | 0700 | node:node |
| `/home/node/.openclaw/workspace` | 0700 | node:node |
| `/home/node/.config/openclaw` | 0700 | node:node |

Guarded by a self-verifying `stat -c '%U:%G %a' ... | grep -qx` chain.
Confirms D2's volume list: `.openclaw` + `.config/openclaw`. The harness
template must `install -d` these before the mounts land.

### Build ARGs worth carrying as harness knobs

- `OPENCLAW_EXTENSIONS` — **bundled plugin/extension selection at BUILD time**
  (e.g. `"diagnostics-otel,matrix"`). Channels are extensions → channel
  availability may be build-time-determined.
- `OPENCLAW_IMAGE_APT_PACKAGES` (legacy alias `OPENCLAW_DOCKER_APT_PACKAGES`)
- `OPENCLAW_IMAGE_PIP_PACKAGES`
- `OPENCLAW_INSTALL_BROWSER` → also sets
  `PLAYWRIGHT_BROWSERS_PATH=/home/node/.cache/ms-playwright` (volume candidate)
- `OPENCLAW_INSTALL_DOCKER_CLI` — docker CLI for the native sandbox. Stays OFF.

### P1 install-method decision + its open question

A source build is NOT expressible as a harness fragment (needs the repo as
build context). → install `npm i -g openclaw`, matching
`version: {resolver: npm, package: openclaw}`.
**GATING UNKNOWN:** does the npm package ship the bundled extensions, or are
they build-time-only? If npm does not bundle them, channel availability differs
from the official image and needs a plan. Resolve before writing the template.

## Upstream native sandbox

> **SOURCING WARNING.** Most of this block came from deepwiki and only the
> items marked VERIFIED were checked against real files. **deepwiki is not a
> source of truth** (user, 2026-07-28; see `feedback_deepwiki_not_canon`). It
> was wrong twice in this very initiative: it missed the container-detection →
> Homebrew coupling entirely (the thing that actually mattered), and it claimed
> `OPENCLAW_GATEWAY_BIND` overrides the bind mode when the shipped package has
> ZERO code hits for it. Treat every unmarked claim below as a lead to check,
> never as fact.

Three sandbox images under `scripts/docker/sandbox/`:
- `Dockerfile` → `openclaw-sandbox:bookworm-slim` — **VERIFIED** against the
  fetched file: `FROM debian:bookworm-slim@sha256:f9c6a2fd…` (deepwiki omitted
  the digest pin), pkgs `bash ca-certificates curl git jq python3 ripgrep`,
  `useradd --create-home --shell /bin/bash sandbox`, `USER sandbox`,
  `WORKDIR /home/sandbox`, `CMD ["sleep","infinity"]`.
  Image names **VERIFIED** present in dist: `openclaw-sandbox:bookworm-slim`,
  `openclaw-sandbox-common:bookworm-slim`, `openclaw-sandbox-browser:bookworm-slim`.
  `capDrop ?? ["ALL"]` **VERIFIED** in dist; `readOnlyRoot` exists as a config
  field. Remaining claims below (mode/scope/backend enums, `network: none`,
  `user 1000:1000`, CDP/VNC ports) are UNVERIFIED deepwiki output.
- `Dockerfile.common` → `openclaw-sandbox-common:bookworm-slim`, adds
  go/rust/node/pnpm/bun and conditionally homebrew (`linuxbrew` user).
- `Dockerfile.browser` → `openclaw-sandbox-browser:bookworm-slim`, CDP 9222 /
  VNC 5900, `--user-data-dir=$HOME/.chrome`.

Config `agents.defaults.sandbox` (default OFF): `mode` off|non-main|all;
`scope` agent|session|shared; `backend` docker|ssh|openshell;
workspace access none|ro|rw. Docker defaults are strict — `network: none`,
`readOnlyRoot: true`, `capDrop: ["ALL"]`, `user 1000:1000`.
`OPENCLAW_SANDBOX=1` triggers `scripts/docker/setup.sh` to build the image.

**Decision: keep `mode: off` under clawker.** It needs the Docker socket, which
punctures clawker's boundary to re-solve one layer down what clawker solved one
layer up. Good article contrast material.

## Firewall regex bug found + fixed 2026-07-28

`.clawker.yaml` had `path: ~/openclaw/(/.*)?` for `raw.githubusercontent.com`.
`~` = full-string-anchored RE2: matches `/openclaw/` and `/openclaw//x`, never
`/openclaw/<repo>/...` (after the literal, the optional group must itself start
with `/`). Symptom: HTTP 403, `content-length: 10` (`Forbidden\n`),
`server: envoy` — a clawker deny, not GitHub's.
Fixed to `~/openclaw(/.*)?` (org-wide; user: "they are a trusted organization").
Correct neighbor pattern to copy: `~/nvm-sh/nvm(/.*)?`.

## Container detection ↔ Homebrew coupling — READ FROM SHIPPED DIST 2026-07-28

Verified against the npm tarball's built `dist/` (deepwiki said detection is
"separate from sandbox" — true, but it MISSED this coupling entirely; source is
canon). Tarball extracted to scratch: `oc/package/dist/`.

`container-environment-CNsJSTpY.js` — `isContainerEnvironment()`, cached per
process, NO override flag:
- `FLY_MACHINE_ID` + `FLY_APP_NAME` both set, or
- any of `/.dockerenv`, `/run/.containerenv`, `/var/run/.containerenv`, or
- `/proc/1/cgroup` matches
  `/\/docker\/|cri-containerd-[0-9a-f]|containerd\/[0-9a-f]{64}|\/kubepods[/.]|\blxc\b/`

clawker containers trip this immediately. Three consequences, only ONE of which
is overridable:

| consumer | effect | overridable? |
|---|---|---|
| `net-*` / `run-*` | default bind → `auto` (0.0.0.0) for port-forwarding; auth then REQUIRED | yes — `OPENCLAW_GATEWAY_BIND` / `gateway.bind` |
| `onboard-skills-*:115` | brew-only skills **filtered out of the installable list** when brew absent | **no** — only by making brew present |
| `setup.finalize-*:175` | `containerWithoutUserSystemd` → daemon-install path notes systemd unavailable | n/a (hence `--no-install-daemon`) |

`onboard-skills-DhXO03HN.js:115`:
```js
const inLinuxContainer = process.platform === "linux" && isContainerEnvironment();
if (inLinuxContainer && baseInstallable.length > 0 && !await detectBrewOnce()) {
  const hiddenBrewOnly = baseInstallable.filter(isBrewOnlyInstallableSkill);
  installable = baseInstallable.filter((skill) => !isBrewOnlyInstallableSkill(skill));
```
Skills are INVISIBLE, not failing — the "click install in the webui" flow
silently lacks options. `install-DobYJYZi.js:467` (`resolveBrewMissingFailure`)
swaps in a container-specific message: "Build a custom image with Homebrew or
install `<formula>` manually…"

**brew is load-bearing beyond brew formulas:** `ensureUvInstalled` shells out to
`brew install uv`, so no brew ⇒ no uv ⇒ python/uv-backed skills also die.

**Not a macOS-only thing.** `HOMEBREW_PROMPT_PLATFORMS = new Set(["darwin",
"linux"])`, and the non-container Linux failure message points at brew.sh.
Homebrew-on-Linux (ex-Linuxbrew) is first-class; container-ness alone
suppresses it. Their rationale is sound — a runtime brew install in a container
is ~500MB, needs its own user, and evaporates on recreate.

→ **Baking Linuxbrew into the harness image at build time is the
upstream-sanctioned fix**, not a workaround. openclaw-deploy read this
correctly. Once `detectBrewOnce()` succeeds the container branch never fires.

### CORRECTION to the "upstream ships no brew ⇒ concern dissolves" claim

That earlier reading was backwards. Upstream not shipping brew is precisely
what makes container detection amputate those skills. The user's whole-home
instinct was tracking a real constraint — wrong instrument, right problem.

### Real clawker contract gap (file separately)

Brew installs land in `/home/linuxbrew/.linuxbrew/Cellar` — OUTSIDE container
home. `VolumeSpec.Path` is home-relative, so harness `volumes:` cannot persist
it, and runtime-installed skill deps die on every container recreate.
Two problems, distinct:
1. **Visibility** — bake brew into the harness image (P1 work).
2. **Persistence** — needs either `VolumeSpec` extended to absolute paths
   (clawker core change) or a `-v` bind at the openclaw-deploy layer (which is
   what that repo already did).

## clawker harness contract (what a harness may declare)

`harness.yaml` → `config.Manifest`:
- `version:` {resolver: npm|github-release|none, package, tag_prefix}
- `stacks:` [] — addresses resolved bare/qualified
- `volumes:` [{name, path}] — **named Docker volumes only**, path is
  container-home-relative. `clawker.<project>.<agent>-<harness>.<name>`
- `seeds:` [{file (under assets/), dest, apply: copy-if-missing |
  copy-if-missing-or-empty | json-merge}] — applied by CP init on first boot
- `staging.copy:` [{src (host, expands ~ $VAR ${VAR:-def}), dest, json_keys,
  skip, json_rewrites}] — create-time host→container copy; dest MUST fall
  under a declared volume
- `staging.mounts:` [{src, dest}] — **live host bind mount** into container
  home (`workspace.GetHostStateMount`, src must be absolute). This is the
  existing mechanism for "always bind mounted" harness state.
- `managed_prompt:` {dest, owner, mode} — build-time briefing bake
- `egress:` [] — harness floor, composed with project rules

`Dockerfile.harness.tmpl` block slots (user-ratified names):
`root_after_stacks`, `user_after_stacks`, `user_after_shell_switch`,
`root_before_entrypoint`, `cmd`.

Bundle repo shape (`clawker-test-bundle`): `.clawker-bundle/bundle.yaml`,
`harnesses/<name>/{harness.yaml,Dockerfile.harness.tmpl,assets/}`,
`stacks/<name>/{stack.yaml,Dockerfile.stack-{root,user}.tmpl}`.
Declared in project config as `bundles: [{url, ref}]` → `clawker bundle install`
→ `clawker build -t <ns>.<bundle>.<component>`.

## clawker runtime facts that matter here

- `clawker container run` supports `-p/--publish`, `-P`, `--expose`,
  `--detach`, `--restart`, `--health-*`, `-v`, `--mode bind|snapshot`.
  So a published, restarting, detached daemon container is already possible.
- **Workspace mounts at the SAME absolute path as the host**
  (`container_create.go:2092` — "Mount at host absolute path for harness
  session /resume compatibility"), and becomes the container `WorkingDir`.
  Consequence: a workspace-relative state path cannot be baked as a static
  `ENV` in the harness image; it must be resolved at runtime (`$PWD`) or
  supplied via `agent.env` / `pre_run`.
- Container home is `/home/<ContainerUser>` (`consts.ContainerHomeDir`).
- Lifecycle volume `$HOME/.clawker` carries the post_init marker → `post_init`
  runs ONCE per (harness-scoped) config-volume set, not per container start.
  `pre_run` runs each start.
- Harness volumes are harness-scoped: `clawker.<project>.<agent>-<harness>.<name>`.

## Open risks (unresolved, feed the plan)

1. **MITM vs pinned clients.** clawker always MITMs TLS (no SNI passthrough,
   `project_mitm_load_bearing`). WhatsApp/Signal/Matrix clients may pin or use
   non-HTTP transports → may not survive. Discord/Telegram/Slack bots use plain
   TLS+wss and should. Needs live UAT per channel.
2. **Daemon-shaped harness is unproven.** Every shipped harness is an
   interactive CLI whose CMD exits. clawkerd PID-1 + AgentReady dispatch with a
   long-lived non-TTY CMD, `--detach`, and restart policy needs live UAT.
3. **Docker socket.** OpenClaw's own sandbox needs it; enabling it in clawker
   punctures the isolation boundary. Default should be OFF — clawker container
   IS the sandbox.
4. **Git-tracked state contains secrets** by default (`auth-profiles.json`,
   `.env` w/ gateway token). `--secret-input-mode ref` + gitignore is the
   mitigation; needs an explicit secrets policy in the fork repo.
5. **Egress floor is large and channel-dependent.** Must be derived from
   observed blocked traffic (firewall UAT loop), never guessed.

## Secrets — verified 2026-07-28 (supersedes the state-path assumptions above)

Checked because openclaw-deploy-era secrets were plaintext-in-config-dir and
the user suspected a keychain overhaul. There was an overhaul; it did not go
toward keyrings.

- **No Linux keyring/libsecret integration.** `docs.openclaw.ai/gateway/security`
  does not mention keychain, libsecret, or keyring at all. macOS Keychain
  appears only in narrow external-CLI credential paths, and read-only/status
  paths pass `allowKeychainPrompt: false` (file-backed external CLI creds only).
  → the `libsecret-tools` apt package in openclaw-deploy's Dockerfile is
  probably vestigial. Drop from the harness unless P1 shows otherwise.
- **SecretRef providers: `env`, `file`, `exec`.** The actual improvement.
  `--secret-input-mode ref` writes `{source:"env", provider:"default",
  id:"<VAR>"}`; `file`/`exec` allow host-side secret managers.
- **Auth profiles moved into SQLite.** `auth-profiles.json` is now a LOGICAL
  KEY, not necessarily a file — backing store is the agent's
  `openclaw-agent.sqlite`.
- **`$OPENCLAW_STATE_DIR` is the real one — RESOLVED 2026-07-28.** See the
  env-var table below.
- Docs posture: "Assume anything under `~/.openclaw/` may contain secrets or
  private data"; 600 files / 700 dirs; "treat disk access as the trust
  boundary"; full-disk encryption on the gateway host.
- Headless/Docker credential sources per docs: gateway process env,
  `~/.openclaw/.env`, or config `env` block. Workspace `.env` files are
  BLOCKED from overriding sensitive vars.

### Consequence for P5 (openclaw-deploy git-tracked bind mount)

Secrets live inside SQLite blobs (`agents/<id>/agent/openclaw-agent.sqlite` =
model auth profiles; `state/openclaw.sqlite` = MCP OAuth tokens), not only in
named credential files. A `.gitignore` that denylists credential filenames
will leak. The git-tracked state dir must **allowlist** what is committed.
Settle before P5 ships, not after. D2 (named volumes) is unaffected.

## Env vars — COUNTED in the published npm package 2026-07-28

`grep -rl` over the extracted tarball. **Docs are not a reliable guide here:
four documented vars are not read by the shipped build at all.**

| var | code hits | verdict |
|---|---|---|
| `OPENCLAW_STATE_DIR` | 53 | **the state-dir var.** `env.OPENCLAW_STATE_DIR \|\| path.join(process.env.HOME, ".openclaw")` |
| `OPENCLAW_GATEWAY_TOKEN` | 46 | read |
| `OPENCLAW_GATEWAY_PORT` | 10 | read |
| `OPENCLAW_NO_RESPAWN` | 5 | read |
| `PNPM_HOME` | 4 | read |
| `OPENCLAW_WORKSPACE_DIR` | 2 | read |
| `NODE_COMPILE_CACHE` | 2 | read |
| `OPENCLAW_CONFIG_DIR` | **0** (docs only) | docker-compose interpolation var naming the HOST bind-mount side; the app never reads it |
| `OPENCLAW_GATEWAY_BIND` | **0** (docs only) | deepwiki claimed it overrides bind — WRONG for this build |
| `OPENCLAW_SUPERVISOR_MODE` | **0** (absent even from docs) | the CLI reference describes it; the package has no trace |
| `OPENCLAW_PREFER_PNPM` | **0** (one docs page) | build-time flag for upstream's own image build |

Consequences:
1. Answers the state-dir question. Harness fixed in `d49fe0f` — it had set
   the dead var and omitted the live one (invisible, since the default
   resolves to the same path).
2. **Container detection's BIND consequence has NO env override.** Only
   `gateway.bind` in config does. The earlier note that
   `OPENCLAW_GATEWAY_BIND` is an escape hatch is wrong.
3. Lesson for P3: verify every OpenClaw env var against the package before
   relying on it. The docs list vars the build does not read.

## npm 11.16 silently blocks install scripts — verified 2026-07-28 (`853ae15`)

`npm install -g openclaw` on `node:24-bookworm-slim` (npm 11.16.0) exits 0 but
SKIPS lifecycle scripts, warning only:

```
npm warn allow-scripts 4 packages have install scripts not yet covered by allowScripts:
npm warn allow-scripts   openclaw@2026.7.1-2 (preinstall: …; postinstall: node scripts/postinstall-bundled-plugins.mjs)
npm warn allow-scripts   @google/genai@2.10.0, protobufjs@7.6.3, tree-sitter-bash@0.25.1
```

OpenClaw's postinstall does dist hygiene, plugin-registry migration, and a
baileys (WhatsApp) patch — upstream's Dockerfile runs it explicitly, so
skipping it is a real divergence. Note it does NOT install plugin deps
("Plugin package dependencies are installed only by explicit plugin
install/update flows, never postinstall").

npm 11.16 interface (from `npm install --help`):
`--allow-scripts <package-list>` (comma-separated allowlist),
`--strict-allow-scripts` (warning → hard failure),
`--dangerously-allow-all-scripts` (bypass), plus `npm approve-scripts`.

Harness now uses the explicit allowlist + strict, so a future script-bearing
dependency FAILS the build and names itself instead of being silently dropped.

**Applies to any npm-installing harness in this repo** — claude and opencode
install via their own installer scripts so they dodge it, but any future
`npm install -g` harness hits the same silent skip.

Caveats worth keeping straight:
- `dist/postinstall-inventory.json` SHIPS in the tarball, so its presence is
  NOT proof the postinstall ran. What is proven: no warning + exit 0 under
  strict = the policy was satisfied.
- `tree-sitter-bash` ships prebuilds for every platform, so `node-gyp-build`
  was not load-bearing. It is on the allowlist because strict mode fails on
  ANY unapproved script, not because the build needs it.

## Verified working (throwaway containers, 2026-07-28)

Not yet run under clawkerd/CP — these are direct `docker run` checks:
- `npm install -g openclaw@2026.7.1-2` → 309 packages in ~9s, no native-build
  or OOM trouble; `/usr/local/bin/openclaw` → `../lib/node_modules/openclaw/openclaw.mjs`.
- `openclaw --version` → `OpenClaw 2026.7.1-2 (0790d9f)` with NO config present.
- `openclaw gateway --help` responds; confirms `--allow-unconfigured`, `--auth`,
  `--bind` flags exist. Help text says bind "Defaults to config gateway.bind
  (or loopback)" — generic string; the container-detection→auto path lives in
  `run-*.js` and is not contradicted by it.
- Homebrew: bottles pour normally through a symlinked prefix; volume anchor
  mounts on the canonical path (see the plan memo's Gap 1 section).

## Hook ordering — verified 2026-07-28 (reshapes P3)

`internal/config/schema.go:170-171`: `post_init` runs "after container starts
but **before the harness launches**", once per creation; `pre_run` runs every
start "**right before the harness CMD runs**". Wiring:
`container_create.go:2011` (injectPostInitIfConfigured),
`container_start.go:195` (pre-run).

**There is no in-container hook that fires after the gateway is up.** Anything
needing a live gateway API cannot be post_init or pre_run. Not a clawker gap —
the hooks are correctly named. Split:

| when | mechanism | what |
|---|---|---|
| pre-gateway | `post_init` | `openclaw onboard --non-interactive --accept-risk`, boot-read `config set` |
| post-gateway | host-side script: poll `/healthz`, then `clawker exec` | channel wiring, skills, gateway-API-owned setup |

The post-start script is openclaw-deploy's job (P5) — successor to Pulumi's
`gateway-post-init.ts`. Harness stays vanilla.

## Daemon-shaped CMD — verified 2026-07-28 (risk DOWNGRADED)

Initial "daemon harness is unproven" framing was overweighted; user pushed
back correctly. Every shipped harness CMD is already a persistent process
(`claude` runs until exit). Confirmed support:

- `clawkerd/spawn_unix.go:292` handles non-TTY explicitly ("`docker run`
  without `-ti`, CI, piped").
- `--detach` is a documented run mode.
- `Dockerfile.harness-image.tmpl:178-179`: `ENTRYPOINT ["/usr/local/bin/clawkerd"]`
  is fixed; the harness fills only the `cmd` block. openclaw-deploy's
  `entrypoint.sh` has no successor — CoreDNS→CP firewall, gosu drop→clawkerd,
  perm fixes→named volumes, sshd/filebrowser→dropped.

Remaining deltas are narrow, folded into P1 as checks: daemon exit means
crash-not-done (restart policy), and there is no session to attach to.

Inbound is unaffected by the firewall (eBPF filters egress), so
`-p 127.0.0.1:18789:18789` reaches the Control UI/API normally. `clawker exec
<agent> openclaw ...` covers CLI subcommands with no port. Both, not either/or.
