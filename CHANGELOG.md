# Changelog

All notable, user-facing changes to clawker are documented here. This is the
**curated** changelog — it intentionally covers only the handful of releases
that change the user surface, not every tech-debt or dependency bump. The
exhaustive per-commit list lives in each GitHub release's "Commits" section.

The format follows Keep a Changelog, and clawker adheres to Semantic Versioning.

A release spans many merged PRs and may mix change kinds — Added, Fixed,
Changed, Removed. Each release section lists those subsections directly.

## [2026.7.1] - 2026-07-17

### **⚠ MAJOR UPDATE — BREAKING CHANGES — multi-harness support, bundles**

> **Action required:** images from earlier releases will not work. Custom base images are gone — every image now builds from clawker's pinned Debian base, customized by **stacks**: composable toolchain layers that install a language runtime and its tooling into the image. If you used `build.image` or `build.dockerfile`, recreate what your image provided by listing stacks under `build.stacks` in `clawker.yaml` (`go`, `node`, `python`, `rust`, `java`, `ruby`, `cpp`, and `dotnet` ship built in — `clawker stack list` shows everything available, including bundled ones), adding apt packages under `build.packages`, and expressing any remaining setup as `build.instructions`. Then run `clawker build` in every project. Stock configs just rebuild.
>
> I have not had time to properly vet this release's code, stability, or security. It's a major change to how clawker builds and runs agents, but I rushed it out the door to capitalize on temporary Fable access. I've decided to release it now, but expect rough edges — it may well have a lot of bugs. Please [report issues](https://github.com/schmitthub/clawker/issues) so I'm aware of them; I'll be diligent about quick patch releases over the near future to iron out the kinks. If something breaks your workflow, you can always roll back to the last release: `curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | CLAWKER_VERSION=v0.12.11 bash`. The heart of it is an extensibility system for your own container dev stacks, monitoring dashboards, and most importantly harnesses, all distributable as bundles you can install from a git repository or a local directory. Out of the box you get the original Claude Code experience plus an embedded Codex PoC.
>
> See https://github.com/schmitthub/clawker-bundle-example for ongoing experimental harnesses, stacks, and monitoring extensions (including pi and opencode); they'll fold into the shipped built-ins as they mature.

- **Added:** Multi-harness support — clawker runs Claude Code, OpenAI Codex, or any harness you supply. Projects build one shared base image with per-harness variants: pick the harness at build time with `clawker build -t <harness>` and at run time with the `@:<harness>` selector, or set a project default in `build.harness`. The default is `claude` — the original experience, unchanged. `codex` ships as a PoC; supply your own via the harness directory or an installed bundle. Try `codex` out today with `clawker build -t codex && clawker codex dev`
- **Added:** Extensible components — stacks, harnesses, and monitoring extensions are the three component types behind the new build/run model, and all of them are user-suppliable: author your own or install them from bundles. Stacks are composable toolchain layers for the image (`go`, `node`, `python`, `rust`, `java`, `ruby`, `cpp`, and `dotnet` ship built in). Monitoring extensions package dashboards, index templates, and ingest pipelines for the observability stack; `clawker monitor up` seeds the ones a project selects under `monitor.extensions` (defaults to the built-in `claude-code` extension; set an explicit empty list, `extensions: []`, to opt out). Inventory each type with `clawker stack list`, `clawker harness list`, and `clawker monitor extensions`.
- **Added:** Bundles — install stacks, harnesses, and monitoring extensions distributed as a git repository or a local directory. Declare sources under a `bundles:` key in `clawker.yaml` and manage them with `clawker bundle install | list | prune | remove | update | validate`. Bundled components are addressed by their qualified `namespace.bundle.component` name. Bundles are validated on install and with `bundle validate` — both run every component through the same checks the consuming commands apply, so a broken bundle fails at publish or fetch time instead of at build time. **Use caution with third-party bundles:** their components directly shape image builds and agent runtime behavior. Only install bundles from sources you trust, review what they contain (validation checks structure, not intent), stay diligent on updates, and consider forking a third-party bundle repo so you control exactly what you've reviewed.
- **Added:** Per-harness aliases `clawker claude <agent>` and `clawker codex <agent>` — each selects its harness and skips its permission prompts (`--dangerously-skip-permissions` for Claude Code, `--yolo` for Codex).
- **Changed:** The `go` and `wt` aliases no longer run Claude Code specifically — they launch your **default harness** and no longer pass `--dangerously-skip-permissions` (a Claude Code-only flag that would break other harnesses). For the previous experience, run `clawker claude <agent>` instead, or append your harness's flag: `clawker go dev --dangerously-skip-permissions`.
- **Changed:** Host credentials are no longer copied into containers (the `agent.claude_code.use_host_auth` key is gone). Authenticate once inside the container — browser OAuth flows are proxied to your host browser — and the login persists in the harness config volume.
- **Removed:** `build.image`, `build.dockerfile`, and `build.context` — custom base images and user-supplied Dockerfiles are no longer supported; every image builds from clawker's pinned Debian base. Customize with `build.stacks`, `build.packages`, `build.instructions`, and `build.inject`. Existing keys are removed automatically with a notice on first load.

## [0.12.11] - 2026-07-02

- **Added:** YAML schema definitions for `clawker.yaml` files and `settings.yaml` files, for IDEs that support JSON Schema validation.  
- **Added:** Comment preservation to yaml backed configuration.  

## [0.12.10] - 2026-06-29

- **Added:** `nvm` to base Dockerfile template

## [0.12.9] - 2026-06-23

- **Added:** Egress path rule RE2 regex support. Add `~` at the beginning of a `path` rule to match it as a full-string regex instead of a prefix — anchor exactly and use alternation.
  - ex: Add two paths sharing a common segment, optional trailing slash: `~/repos/(clawker|anthropic)/?`
  - ex: Add the exact path without trailing slash: `~/schmitthub/clawker$`

## [0.12.8] - 2026-06-20

- **Fixed:** TypeScript preset's `pre_run` script now tolerates `npm install` failures instead of exiting the container, which could happen on transient registry errors or if the project hasn't set up its dependencies yet.

## [0.12.7] - 2026-06-19

### **⚠ ACTION REQUIRED — rebuild all agent container images**

> Run `clawker build` in every project after updating. Containers built before 
> this release run an outdated internal runtime and will be stopped automatically; 
> they will not work until rebuilt.

- **Fixed:** agent boot freezing on init/boot command failures (e.g. `post_init`,
  `pre_run`). clawkerd now prints the failure to stdout and exits, and the control
  plane reaps the failed container if it doesn't shut down gracefully.

## [0.12.6] - 2026-06-18

- **Fixed:** CLI OTEL logger no longer blocks command exit for up to 5 seconds when monitoring is down. 

## [0.12.5] - 2026-06-17

- **Fixed:** Agent prompt causing agents to forgo setting up branch upstream tracking

## [0.12.4] - 2026-06-17

- **Added:** Release notes notifications.

## [0.12.3] - 2026-06-15

### Fixed

- **Snapshot workspaces now include git history.** Ephemeral snapshot
  workspaces copy the project's `.git` directory, so `git log`, `git diff`, and
  branch operations work inside the container. Creating a snapshot workspace on
  top of a git worktree is now rejected up front with a clear error instead of
  producing a broken checkout.

## [0.12.0] - 2026-06-11

### Added

- **Command aliases, with `clawker go` and `clawker wt` built in.** clawker now
  ships shortcut commands you can use right away: `clawker go <agent>` launches a
  throwaway interactive agent against the current project, and
  `clawker wt <agent> <branch>` does the same inside a git worktree for that
  branch. Manage aliases with the new `clawker alias` command (`list`, `set`,
  `delete`, `export`), and define your own subcommand shortcuts under `aliases:`
  in `clawker.yaml` — with positional `$1..$N` argument substitution — invoked
  like any built-in command.

## [0.11.0] - 2026-06-10

### Fixed

- **Worktree containers protect the host repository.** When the workspace is a
  git worktree, the main repository's `.git/hooks` and `.git/config` are mounted
  read-only so container-side writes cannot execute code on the host. Go builds
  inside worktree containers no longer fail on VCS stamping, and starting a
  second container against an already-checked-out branch is refused instead of
  corrupting the worktree.
- **Worktrees track their upstream branch.** `clawker worktree add` now pulls
  remote-tracking branches with their upstream set, so `git pull`/`git push`
  work without manual `--set-upstream`.

## [0.10.3] - 2026-06-09

### Fixed

- **Expired-but-refreshable host login is forwarded.** Claude Code credentials
  copied from the host are injected even when the host access token has expired,
  as long as it is still refreshable — the container refreshes on first use
  instead of forcing a fresh `/login`.

## [0.10.0] - 2026-06-06

### Added

- **`clawker firewall refresh`.** Live-apply egress edits made to
  `clawker.yaml` (`security.firewall.add_domains` and `security.firewall.rules`)
  into the running firewall without restarting any container. Add/update only —
  removing a rule still uses `clawker firewall remove`.
- **Every-start container hook (`pre_run`).** The `agent.pre_run` script runs on
  every container start (not just the first), complementing the once-only
  `post_init` hook.

## [0.9.0] - 2026-05-18

### Changed

- **Monitoring stack moved to OpenSearch + Prometheus.** The bundled
  observability stack now uses OpenSearch (logs) and Prometheus (metrics) in
  place of the previous Loki/Jaeger/Grafana stack, with a preconfigured
  OpenSearch Dashboards setup applied by `clawker monitor up`.

## [0.8.0] - 2026-05-12

### Removed

- **Looping ("ralph") mode has been retired.** The agent loop subsystem is gone;
  drive iteration through your own workflow or Claude Code directly. A
  managed-settings `PATH` regression introduced alongside it is also fixed.

## [0.7.0] - 2026-04-03

### Added

- **`clawker skill` commands.** Manage Claude Code plugin skills directly from
  clawker — list, add, and remove skills for the agent's Claude Code
  installation.

## [0.6.0] - 2026-03-26

### Changed

- **Preset-based guided `init`.** `clawker init` was rewritten as a guided,
  preset-based setup flow, making first-run project configuration substantially
  faster and less error-prone.

## [0.5.0] - 2026-03-20

### Added

- **Global egress firewall stack.** A shared Envoy + custom CoreDNS + eBPF
  egress-enforcement stack governs outbound traffic from every agent container,
  with per-project allow rules declared in `clawker.yaml` under
  `security.firewall`.

## [0.3.0] - 2026-02-24

### Added

- **Host-path workspace mounts.** Run an agent against a live bind mount of the
  host project directory, so changes made in the container are immediately
  visible on the host (the alternative to ephemeral snapshot workspaces).

## [0.1.0] - 2026-02-11

### Added

- **Git worktree support.** Run an agent in an isolated git worktree keyed to a
  branch, including slashed branch names, via `clawker worktree add` and
  `clawker run --worktree`.
- **Git credential forwarding.** SSH agent, GPG agent, and HTTPS git
  credentials are forwarded from the host through a socket bridge, so commits
  sign and private repositories clone inside the container.
