# OpenClaw harness for clawker — initiative plan

Companion to `initiative_openclaw_harness/recon` (facts) — this file holds
decisions and phases. Branch: `feat/openclaw-harness` (clawker repo).
Started 2026-07-28.

## Goal

Run OpenClaw as a clawker-contained, deny-by-default-egress agent. Two
deliverables, deliberately separated:

1. **`harnesses/openclaw` in `clawker-bundle-example`** — the POC harness,
   candidate for embedding alongside claude/codex later.
2. **`openclaw-deploy` (later)** — the full-featured forkable setup repo:
   local + IaC, bind-mounts, Tailscale, the works.

Plus a practical "sandbox your OpenClaw in 5 minutes" article in Mintlify.

## Ratified decisions (user, 2026-07-28)

| # | Decision | Rationale (user's words, compressed) |
|---|---|---|
| D1 | Harness POC lives in **clawker-bundle-example**, not openclaw-deploy | openclaw-deploy will be "a full featured setup not just a harness" — shipping the harness there drags "a lot of other crap" onto users who only want the harness. Bundle repo = candidate for embedding later. |
| D2 | State = **named volumes** (standard harness default) | Harness stays conventional. openclaw-deploy separately layers bind mounts — it already binds the entire openclaw user homedir (necessary for homebrew), so memories/data survive container lifecycles. Two layers, not one compromise. |
| D3 | IaC = **full pivot**, but deferred | clawker owns the firewall; Envoy/CoreDNS/bypass sidecars get deleted. Tailscale "figure out later — we'll prob just have to include it in the harness bundle run stage and set it all up ourselves." **First phases are local-only.** |
| D4 | Article = **practical 5-minute sandbox guide** | Top-of-funnel traffic. The precursor→clawker architecture table stays available as later material, not the lede. |

## Phases

### P1 — openclaw harness in clawker-bundle-example — **BUILT, PR OPEN**

Branch `feat/openclaw-harness` in the submodule; PR
https://github.com/schmitthub/clawker-bundle-example/pull/1 (not merged;
clawker submodule pointer NOT bumped yet). Shipped:
`harnesses/openclaw/{harness.yaml,Dockerfile.harness.tmpl}`, README section,
calver switch (`2026.7.1`).

Verified: `bundle validate --strict` PASS; rendered via
`bundler.GenerateHarness` against an in-place `path:` bundle decl — the
`.config` pre-create lands before the engine volume block, `chmod 0700` runs
as owner after it, and the version ARG sits below the brew layer;
`go test ./internal/bundler/` PASS.

NOT verified — no container built or run yet. That is P2/P3.

Original scope notes follow.

### P1 (original scope)
`harnesses/openclaw/{harness.yaml,Dockerfile.harness.tmpl,assets/}`

- `version:` npm resolver, package `openclaw`.
- `stacks:` `[node]`.
- `volumes:` `.openclaw` (config/creds/sqlite/workspace) — `.config/openclaw`
  REMOVED 2026-08-01 (legacy-sidecar-only path, fresh installs never write it;
  see recon correction + bundle PR #2)
  (`OPENCLAW_AUTH_PROFILE_SECRET_DIR`). Audit whether pnpm store / plugin
  package roots / `.cache` need their own.
- `Dockerfile.harness.tmpl`: npm global install pinned to `{{.HarnessVersion}}`,
  CLI symlink from `$(npm root -g)/openclaw/dist/entry.js`, `OPENCLAW_*` ENV
  (`CONFIG_DIR`, `WORKSPACE_DIR`, `GATEWAY_PORT`, `GATEWAY_BIND`,
  `PREFER_PNPM=1`, `NO_RESPAWN=1`), `cmd` block = `openclaw gateway run`.
- OS deps upstream's image carries: `gosu libsecret-tools build-essential
  ripgrep jq`. Decide harness `root_after_stacks` vs project `build.packages`.
- **Open:** homebrew. Upstream installs linuxbrew for skill deps and it's the
  reason openclaw-deploy binds the whole homedir. Decide: skip for POC, add a
  `brew` stack to the example bundle, or accept degraded skill installs.
- **Open:** `managed_prompt:` dest — where does OpenClaw read managed/system
  context from? Its workspace `AGENTS.md` sits inside a volume, so it is
  seed-able but not build-time bake-able. May be absent for the POC.
- `egress:` floor seeded from openclaw-deploy `config/domains.ts`, then
  narrowed/widened by P3 observations. Never guessed.

### P2 — daemon-shape checks (DOWNGRADED from risk phase → P1 checklist)
Original "unproven daemon harness" framing was wrong and the user said so.
`claude` is already a persistent CMD; `clawkerd/spawn_unix.go:292` handles
non-TTY; `--detach` is documented. Remaining narrow checks, folded into P1:

- `clawker run --detach -p 127.0.0.1:18789:18789 --restart unless-stopped @:openclaw`.
- Daemon exit = crash, not done — restart policy + exit-code propagation
  through clawkerd PID 1.
- `clawker exec <agent> openclaw config set ...` (no `--`).
- Container `--health-cmd` against `/healthz`; `/readyz` for readiness.
- Log capture: gateway writes rolling logs to `/tmp/openclaw/` AND stdout.
- No session to attach to — confirm that's benign for the boot reporter.

### P3 — onboarding + egress UAT loop
- `agent.post_init` runs `openclaw onboard --non-interactive --accept-risk`
  (exact flag set in the recon memo) + the `openclaw config set` follow-ups.
  post_init is marker-guarded → runs once per harness-scoped volume set.
- Secrets: `--secret-input-mode ref` + `agent.from_env` so keys stay in the
  host env, never in a volume or a repo.
- Gateway token: generate host-side, pass via env; do not bake.
- Firewall discovery loop per `.claude/rules/firewall-uat.md` — run, collect
  actual blocks, widen the floor from observed traffic only.
- Channel order: **Discord + Telegram first** (bot TLS/wss, should survive
  MITM). WhatsApp/Signal/Matrix flagged as MITM-risk — clawker never does SNI
  passthrough (`project_mitm_load_bearing`), so pinned clients may be
  unsupportable. Determine and document, don't paper over.
- Control UI reachable from the host browser: `gateway.bind`,
  `gateway.controlUi.allowedOrigins`, `trustedProxies`, device-auth posture.
- **Decision to make:** OpenClaw's own sandbox (`OPENCLAW_SANDBOX`,
  `agents.defaults.sandbox`) needs the Docker socket. Default OFF — the
  clawker container IS the sandbox. Document why.

### P4 — article
Mintlify standalone page, practical quickstart shape: fork/declare bundle →
set keys → `clawker run` → connect Discord → what's blocked and how to widen.
Own `docs.json` entry. Architecture-table material held back for a follow-up
piece.

### P5 — openclaw-deploy full pivot (after local works)
- Delete `components/envoy*.ts`, `templates/{envoy,coredns,bypass}.ts`; the
  bespoke Envoy/CoreDNS/iptables stack is replaced by clawker's.
- Pulumi provisions VPS + Docker + clawker, then `clawker bundle install` +
  `clawker run`; `clawker.yaml` becomes the single security surface.
- Bind-mount the full openclaw homedir (D2's second layer).
- Tailscale: no clawker equivalent. Likely set up ourselves in the harness/run
  stage. Deferred.

### P6 — embedding decision
If the POC holds, evaluate promoting `openclaw` to an embedded harness next to
claude/codex.

## OUTSTANDING clawker contract gaps (user-raised 2026-07-28, agreed work)

Both surfaced building P1. Neither is expressible in `harness.yaml`, so both
are clawker-side changes, not harness ones. The agent initially flagged these
and moved on — user pushback: "you fudged this quite a bit, basically skipped
any complexity."

### Gap 1 — SOLVED in-contract 2026-07-28 by the volume mount anchor (`8a5d2a3`)

User's idea, and it works: symlink a home-relative path to the out-of-home
prefix purely so `volumes:` has something to name. Docker resolves symlinked
mount destinations, so the volume lands on the real path.

```yaml
volumes: [{ name: brew, path: .linuxbrew }]
```
```dockerfile
RUN ln -s /home/linuxbrew/.linuxbrew /home/${USERNAME}/.linuxbrew
```

VERIFIED end-to-end in throwaway containers:
- `/proc/mounts` → `/dev/vda1 /home/linuxbrew/.linuxbrew ext4 rw,...` — volume
  mounted on the CANONICAL path, not the symlink.
- Image content at the target seeds the volume on first run; runtime writes
  survive into a fresh container.
- Engine's `mkdir -p` no-ops on the existing symlink.
- Engine's `chown -R <user> <path>` touches ONLY the symlink inode — `chown -R`
  defaults to `-P` and does not walk into the target — so the linuxbrew user's
  ownership of the tree is intact. (The feared ~500MB recursive chown does not
  happen.)
- Separately verified: brew pours BOTTLES normally (zstd, binutils, gcc, jq —
  no source builds) as long as the prefix string is the default.

**The anchor is inert by design (user's constraint).** Nothing resolves through
it — `HOMEBREW_PREFIX`/`CELLAR`/`REPOSITORY`, `PATH`, and the `safe.directory`
entry all name the canonical path. brew, openclaw, and the container user are
unaware it exists. Do not point anything at it; do not delete it.

Generalizes to any harness with state outside `$HOME` (`/opt`, `/usr/local`,
SDK caches). This drops the `VolumeSpec` absolute-path change from "needed" to
"would be more honest than a symlink" — still deferred, see below.

### Gap 1 (original) — harness volumes cannot persist state outside the container home

`config.VolumeSpec.Path` is container-home-relative (`harness_schema.go:52-56`).
Homebrew's prefix is `/home/linuxbrew/.linuxbrew` — unreachable.

**Severity is higher than "nice to have."** The openclaw harness BAKES brew
precisely so brew-backed skills stay visible in the Control UI (OpenClaw hides
them otherwise). Without persistence, a user installs N formulae through that
UI and loses every one on container recreate. Baking brew without solving this
surfaces the button that leads to the loss — worse than not having brew.

**Provenance + ruling (user, 2026-07-28):** the home-relative restriction was
not a considered design decision — "it was real dumb i thought to only expose
clawker user relative paths for harness volume mounts lol... an agent did that
and i figured i'd see how it went." So it is a known-wrong constraint, not a
boundary to defend or design around. **DEFERRED: "we can leave it for now."**
Do not re-litigate it as intentional; do not build workarounds that assume it
is permanent. A textbook `no-speculative-constraints` case — a narrowing that
looked safe until a harness needed state outside `$HOME`.

Options:
1. **Extend `VolumeSpec` to absolute paths** (durable; pays off for anything
   installing to `/opt`, `/usr/local`, SDK caches — not just this harness).
   Agreed direction, deferred.
2. Symlink `/home/linuxbrew` → a path under the container home, declare that
   as the volume, keeping the literal prefix string intact so bottles resolve.
   UNVERIFIED — Homebrew resolves its prefix in places and has a history of
   complaining about symlinked prefixes. Test before believing.
3. `-v` bind at the openclaw-deploy layer (P5 only; does not help the harness).

### Gap 2 — a harness cannot declare its runtime shape

`container_create.go:2234-2236`: `WorkingDir` defaults to the workspace
container path unless `--workdir` is passed. `config.Manifest` has no
`workdir:` field. A daemon harness inherits a repo workdir that means nothing
to it.

Related and probably the same fix: **the workspace is mounted unconditionally**
— `--mode` is only `bind|snapshot`, with no opt-out. So an OpenClaw gateway
gets the user's repo bind-mounted at its host path whether or not it wants it.
For a chat-connected agent reachable from Discord/Telegram that is an exposure
question, not just tidiness.

Workaround for testing until fixed (`consts.ContainerUser = "clawker"`):

```bash
clawker run --detach --workdir /home/clawker \
  -p 127.0.0.1:18789:18789 \
  -e OPENCLAW_GATEWAY_TOKEN=$(openssl rand -hex 32) \
  @:schmitthub.test-bundle.openclaw
```

## Pinning: the harness's floating installers are DELIBERATE — do not "fix"

User, 2026-07-28: "clawker pins its own stuff regarding the clawker CLI and its
infra. we largely don't do it for internal container stuff because of version
rot. hence us using latest resolvers etc so that the most recently patched
package is installed." See `feedback_pinning_policy_scope_is_clawker_artifacts_not_user_dockerfile`.

So in `harnesses/openclaw/Dockerfile.harness.tmpl`, both of these are correct
as written and must NOT be converted to pins by a later session reading
clawker's SHA-pinning rules:

- `curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh`
  — floats on HEAD deliberately.
- `ARG OPENCLAW_VERSION={{.HarnessVersion}}` — npm `latest` resolved at
  template-render time, not a frozen literal.

Same reasoning the node stack states outright: a frozen `curl|bash` installer
tag rots into an unpatched installer Dependabot cannot track — the failure mode
CVE-2026-10796 produced for nvm ≤0.40.4. Pinning applies to clawker's OWN
artifacts (CLI, CP/firewall images, CI actions), not to what gets installed
inside a harness image.

Corollary: the `@sha256:` digest on upstream's sandbox base image is a fact
about THEIR Dockerfile, not a standard our harness is measured against.

## Standing risks

Carried from the recon memo: MITM vs pinned channel clients; daemon-shaped
harness unproven; Docker-socket puncture; secrets in persisted state; egress
floor must be observation-derived.
