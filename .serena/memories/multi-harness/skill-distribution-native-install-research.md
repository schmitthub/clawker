# Skill/plugin native-install research per harness (2026-07-14)

Research pass for the **plugin-migration initiative** (deferred off `feat/multi-harness-support` PR by user ruling 2026-07-14: "a full migration of the plugin to its own repo" as a separate initiative after the PR). Governing mandate (user, verbatim): "we aren't stuffing other harness install stuff into a claude marketplace" / "the requirement was to support native canonical installation for each major harness, with the cli command just being a convenience wrapper." Copy-lane-as-primary (`internal/cmd/skill/shared/copy.go`) is REJECTED and is inventory for rework.

Sources: deepwiki grounded in openai/codex, anomalyco/opencode, earendil-works/pi, obra/superpowers, addyosmani/agent-skills source; official codex docs at learn.chatgpt.com/docs/build-skills + /docs/build-plugins (developers.openai.com/codex/* now 308-redirects there); opencode.ai/docs/skills.

## Constraints (user-set, must survive into the migration design)

1. **Distribution unit = PLUGIN** (collection of skills). skills:plugins :: harnesses:bundles.
2. **Plugin content lives in the clawker repo** (`clawker-plugin/clawker-support`) so drift against CLI/scheme changes is detected and the skill auto-updates with scheme changes. Marketplace/index pin lives in a separate repo (`schmitthub/claude-plugins`, `{url, path, sha}`, sha auto-bumped by pinned reusable workflow `update-plugin.yml` gated on `^clawker-plugin/` diffs in main.yml). Any migration must preserve this split — e.g. CI-synced content mirror in the distribution repo, source of truth stays clawker repo.
3. Dir name: the branch's `claude-plugin/`→`clawker-plugin/` rename was REVERTED (user order, commit 4b469931) because the live marketplace `{path, sha}` pin reads path+sha as a pair and the flip can't be atomic with the merge — dir stays `claude-plugin/` until this migration initiative, which owns any rename/move.

## Codex (openai/codex)

- **Full native plugin system, headless CLI**: `codex plugin marketplace add <owner/repo | https-url | ssh-url | local-path>` (supports `--ref`, sparse-checkout paths), then `codex plugin add PLUGIN@MARKETPLACE` (or `--marketplace`). Also `plugin list/remove`, `marketplace list/upgrade/remove`. Installs land in `$CODEX_HOME/plugins/cache/{marketplace}/{plugin}/{version}/`; installed set recorded in `config.toml`. No desktop app required.
- **Plugin manifest**: `.codex-plugin/plugin.json` — `{name, version, description, skills: "./skills/", mcpServers, apps, hooks, interface}`. Multiple skills per plugin supported.
- **Marketplace manifest**: `.agents/plugins/marketplace.json` (repo root or `~/.agents/plugins/` for personal) — `{name, interface, plugins: [{name, source: {source: local, path} | git {url, path, ref_name, sha}, policy: {installation, authentication}, category}]}`.
- **CLAUDE-COMPAT FALLBACK (key finding)**: manifest resolution order is `.codex-plugin/plugin.json` → `.claude-plugin/plugin.json`; marketplace: `.agents/plugins/marketplace.json` → `.agents/plugins/api_marketplace.json` → `.claude-plugin/marketplace.json` (constants `DISCOVERABLE_PLUGIN_MANIFEST_PATHS`, `MARKETPLACE_MANIFEST_RELATIVE_PATHS`; tests confirm). **One repo can serve Claude Code AND Codex as a marketplace.** Git plugin sources carry `{url, path, sha}` — same shape as our existing pin. Exact schema compat of our marketplace.json under codex needs ONE live UAT; fallback if incompatible = add `.agents/plugins/marketplace.json` in codex-native schema to the same repo.
- **Skills discovery** (precedence): project `.codex/skills` → project `.agents/skills` (walk-up incl. parent + repo root) → `~/.agents/skills` → deprecated `$CODEX_HOME/skills` (`~/.codex/skills`) → embedded system (`$CODEX_HOME/skills/.system`) → `/etc/codex/skills`. Per-skill installer exists (`$skill-installer` built-in skill, `install-skill-from-github.py`, installs into `~/.codex/skills`, curated lists `openai/skills/.curated|.experimental`) — per-skill lane, not our unit; plugin lane is the match.
- SKILL.md frontmatter: `name`, `description`; optional `agents/openai.yaml` for UI metadata/invocation policy/tool deps.

## opencode (anomalyco/opencode)

- **No install command for skills.** Discovery dirs (walk-up to git worktree for project level): `.opencode/skills`, `.claude/skills`, `.agents/skills` (project) and `~/.config/opencode/skills`, `~/.claude/skills`, `~/.agents/skills` (global). Note: reads claude + agents dirs natively.
- **Native remote-install surfaces** (both = one declarative entry in opencode.json, opencode does the fetching):
  1. `skills.urls` — array of URLs; each must point at a dir containing `index.json` (JSON array of `{name, version?, files[]}`; one file must be `SKILL.md` or `<name>.md`; files relative to base URL). Fetched at init, cached under `~/.cache/opencode/skills/<url-hash>`, version-checked against `.opencode-version` and self-updated. Also `skills.paths` for local dirs. CAVEAT: V1 (`paths`/`urls`) vs V2 (single sources array) config redesign in flux upstream; V1 still supported in `packages/opencode/src/skill/index.ts`. Not documented on opencode.ai/docs/skills (code-verified only).
  2. `plugin` array — bun-installed at startup (npm names, local paths; cached `~/.cache/opencode/node_modules/`). superpowers documents `"superpowers@git+https://github.com/obra/superpowers.git"` — git specs work via bun's git-dependency support though opencode docs only claim npm/local. A plugin's `config` hook can register skill dirs by mutating merged config (how superpowers ships skills through the plugin lane).
- Skill permission gating exists: `permission.skill` map with globs.

## pi (earendil-works/pi)

- **Native package manager**: `pi install git:host/user/repo[@ref]` | `npm:pkg[@ver]` | local path. Declarative entry appended to `packages` in `~/.pi/agent/settings.json` (project-local with `-l` → `.pi/settings.json`); git clones to `~/.pi/agent/git/<host>/<path>` (project: `.pi/git/`); `npm install` run if package.json present. `pi remove` deletes clone + entry. `pi update` = fetch + hard reset to configured ref (+clean, +npm reinstall); pinned refs never auto-move.
- **REPO-ROOT ONLY — no subdir installs** (monorepo unsupported; DefaultPackageManager clones whole repo, resources discovered from root). Consequence: the clawker repo is NOT directly pi-installable (no root `skills/`, no `pi` manifest → discovers nothing; and full-repo clone would be wrong anyway). pi lane REQUIRES either a distribution repo with content at root or an npm package.
- **Package manifest**: `package.json` `pi` key — `{extensions: [], skills: [], prompts: [], themes: []}` (paths relative to package root, globs OK); keyword `pi-package` recommended. No `pi` key → auto-discovery from conventional dirs `extensions/ skills/ prompts/ themes/` (skills recursed for SKILL.md). One package ships many skills.
- Skills also auto-discovered from `~/.pi/agent/skills/`, `~/.agents/skills/`, project `.pi/skills/`, `.agents/skills/` (walk-up).
- Security: pi packages run with full system access; project-local resources gated on project trust.

## Reference plugins (user-endorsed patterns)

- **obra/superpowers = north star.** ONE repo: harness-agnostic `skills/` + thin per-harness adapters — `.claude-plugin/plugin.json` (+hooks/session-start), `.codex-plugin/plugin.json` (native skill discovery, no hook; `scripts/sync-to-codex-plugin.sh`), `.opencode/plugins/superpowers.js` (plugin-array install; `config` hook registers skills dir, `experimental.chat.messages.transform` injects bootstrap), `.pi/extensions/superpowers.ts` (`pi install git:github.com/obra/superpowers`; `resources_discover` + `context` event). Per-harness tool-mapping files `references/<harness>-tools.md`. Principles: skills name actions not tools; everything ships through the harness's own install mechanism; never edit user files directly. Also covers Antigravity, Cursor, Factory Droid, Copilot CLI, Gemini CLI, Kimi Code (`docs/porting-to-a-new-harness.md`).
- **addyosmani/agent-skills = weaker variant**: claude marketplace native (`/plugin marketplace add addyosmani/agent-skills`), gemini `gemini skills install <git-url> --path skills`, everything else manual copy instructions. Useful datum: gemini CLI has `skills install` with `--path` (subdir) support.

## Cross-harness facts worth designing around

- `~/.agents/skills` is read by codex + opencode + pi (and `.agents/skills` project-level by all three) — the emerging cross-harness standard dir. Writing there directly = the rejected copy lane, but it's the shared fallback surface.
- opencode reads `~/.claude/skills`, but claude PLUGIN installs live under `~/.claude/plugins/...`, not `~/.claude/skills` — a claude plugin install does NOT automatically surface in opencode.
- clawker's in-container agent context = seeded managed prompt, never skills → no bootstrap-injection need (superpowers' JS/TS adapters buy injection we don't need; skills-only adapters may suffice for opencode/pi).

## Design options sketched (NOT ruled on — user deferred; re-present at initiative start)

1. Distribution repo: extend `schmitthub/claude-plugins` into multi-harness distribution repo (keeps marketplace.json for claude+codex; gains CI-synced content mirror at root + `package.json` pi manifest; existing sha-bump workflow extends to content sync) — vs index-only + separate lean content repo — vs npm publish for pi.
2. opencode lane: `skills.urls` index hosted in distribution repo (native self-updating; upstream config flux risk) vs superpowers-style JS plugin vs convention-dir copy (nearest to rejected lane).
3. codex lane: reuse claude marketplace verbatim via fallback (one live UAT) vs codex-native `.agents/plugins/marketplace.json` from day one.
4. `clawker skill --harness X` wrapper = dispatch to native mechanism: claude/codex shell out to their plugin CLIs; pi shells `pi install/remove/update`; opencode writes/deletes the config entry.

Related: `multi-harness/harness-research-opencode-pi` (install/config/auth recon for the harness bundles themselves), auto-memory `project_multi_harness_initiative` (branch state + mandate verbatim).
