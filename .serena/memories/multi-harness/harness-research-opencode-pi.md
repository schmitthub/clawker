# opencode + pi harness recon (live-verified 2026-07-06)

Web recon for the two next shipped harness bundles. Verified against npm packuments/tarballs/binaries + deepwiki; official doc sites (opencode.ai, pi.dev) and github README/api paths were FIREWALL-BLOCKED — items marked UNVERIFIED need live UAT confirmation. Full report archived in session transcript; essentials below.

## opencode

- **Repo moved:** sst/opencode → **anomalyco/opencode** (Anomaly Innovations rebrand). Docs still opencode.ai.
- **Install:** npm `opencode-ai` (latest 1.17.14, several releases/week). Package = wrapper + postinstall picking platform binary from optionalDependencies (`opencode-linux-{x64,arm64}[-musl|-baseline]`; AVX2 sniff for -baseline). Binary = **bun-compiled standalone ELF (~175MB), NO node at runtime** (bun bundled; runtime plugin installs use bundled bun). Alt install: `curl https://opencode.ai/install | bash` (UNVERIFIED — blocked) or direct platform-tarball extraction from registry.npmjs.org (no node needed at build; variant selection is ours). Resolver: npm (`opencode-ai`); alt github-release anomalyco/opencode tag_prefix v (UNVERIFIED).
- **CMD:** `opencode` (TUI), `opencode run [msg]` headless, `opencode serve`. Permission model config-driven (`permission` block, `OPENCODE_PERMISSION` env JSON); default build agent unrestricted. No baked flags (dumb sandbox).
- **Config (XDG):** `~/.config/opencode/opencode.json` (+config.json, tui.json, global AGENTS.md); auth at `~/.local/share/opencode/auth.json`; state `~/.local/state/opencode`; cache `~/.cache/opencode` (bun plugin node_modules). Env: OPENCODE_CONFIG, OPENCODE_CONFIG_DIR, OPENCODE_PERMISSION, OPENCODE_DISABLE_MODELS_FETCH, OPENCODE_DISABLE_CLAUDE_CODE* etc. Project: opencode.json(c), `.opencode/`. Reads AGENTS.md (CLAUDE.md fallback), also `~/.claude/CLAUDE.md` unless disabled. Managed config dir: `/etc/opencode/` (org-level — mirrors clawker managed prompt).
- **Volumes sketch:** config=.config/opencode, data=.local/share/opencode, cache=.cache/opencode (+maybe state).
- **Auth:** env keys (ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, GOOGLE_GENERATIVE_AI_API_KEY, many more) or `opencode auth login` → auth.json. OAuth for Claude Pro/Max exists; **Anthropic OAuth endpoints UNVERIFIED for 1.17.x** (zero claude.ai/console.anthropic.com literals in binary; ecosystem plugin uses claude.ai/oauth/authorize, console.anthropic.com/oauth/*, api.anthropic.com/api/oauth/claude_cli/create_api_key) — confirm via live firewall UAT before hardening floor.
- **Egress floor candidates:** models.dev (catalog; disable via env), registry.npmjs.org (update check + runtime plugin installs), opencode.ai (/theme.json, /config.json, /zen/), api.github.com /repos/anomalyco/opencode/releases/latest (binary-install update check), per-provider hosts operator-added. Share feature = opencode.ai-hosted (UGC sink — path-scope carefully).
- **Policy applied:** NO auth.json staging (credential copying removed project-wide; in-container auth).

## pi

- **Package identity critical:** **`@earendil-works/pi-coding-agent`** (latest 0.80.3, 2026-06-30). `@mariozechner/pi` = old vLLM pods tool (WRONG). `@mariozechner/pi-coding-agent` deprecated at 0.73.1 → earendil scope (Mario Zechner joined Earendil/Armin Ronacher PBC; repo github.com/earendil-works/pi). Unscoped `pi-coding-agent` = squat.
- **Install:** npm only; plain-JS dist/cli.js, **requires node ≥ 22.19** (engines; `legacy-node20` dist-tag lags — use latest + node toolchain; baked NODE_VERSION=24 line satisfies). Resolver: npm.
- **CMD:** `pi` (TUI); headless `pi -p "prompt"`, `--mode json`, `--mode rpc`. **No permission model by design** ("Run in a container") — ideal dumb-sandbox tenant. `pi update --self` self-updates via npm (pin at build).
- **Config:** `~/.pi/agent/` (override PI_CODING_AGENT_DIR): settings.json, auth.json (0600), models.json, keybindings.json, sessions/, extensions/, skills/, prompts/, themes/, git/ (PI_PACKAGE_DIR), AGENTS.md, SYSTEM.md. Project `.pi/`. Instructions: AGENTS.md/CLAUDE.md walk-up + `~/.agents/skills/` (Agent Skills standard). **Single volume: `.pi`.**
- **Auth:** env keys (full documented table) or `/login` OAuth: Claude Pro/Max (claude.ai/oauth/authorize + platform.claude.com/v1/oauth/token — VERIFIED in pi-ai 0.80.3 dist), ChatGPT (auth.openai.com/oauth/* + chatgpt.com/backend-api), Copilot (github.com device + api.individual.githubcopilot.com). Tokens in ~/.pi/agent/auth.json.
- **Egress:** pi.dev (/api/latest-version update check PI_SKIP_VERSION_CHECK=1; /api/report-install telemetry PI_TELEMETRY=0; PI_OFFLINE=1 kills all), registry.npmjs.org (self-update + pi packages), github.com/api.github.com (git packages, /share gists), per-provider hosts (api.anthropic.com, api.openai.com, chatgpt.com, openrouter.ai, generativelanguage.googleapis.com, etc.).
- **Staging sketch:** copy from ${PI_CODING_AGENT_DIR:-~/.pi/agent}: settings.json (consider json_keys allowlist), AGENTS.md, skills/, prompts/, keybindings.json. NOT extensions/ or git/ (arbitrary-code TS with host paths). NOT auth.json (policy).
- **Policy notes:** do NOT seed enableInstallTelemetry:false (seeds = managed config + creds only, never user-config opinions); gate telemetry via egress floor choices instead. Keep floor minimal; widen from observed blocked traffic.

## Open items at bundle-authoring time

1. opencode install lane: npm (node at build, free variant selection) vs direct platform tarball (no node). Codex precedent = standalone installer.
2. opencode Anthropic OAuth hosts → live UAT.
3. pi.dev floor: include /api/latest-version + /api/report-install path rules, or omit host (graceful offline?) — verify startup behavior when blocked (NXDOMAIN) during UAT.
4. Registry keys: `opencode`, `pi`.
