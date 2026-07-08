# Changelog

All notable, user-facing changes to clawker are documented here. This is the
**curated** changelog — it intentionally covers only the handful of releases
that change the user surface, not every tech-debt or dependency bump. The
exhaustive per-commit list lives in each GitHub release's "Commits" section.

The format follows Keep a Changelog, and clawker adheres to Semantic Versioning.

A release spans many merged PRs and may mix change kinds — Added, Fixed,
Changed, Removed. Each release section lists those subsections directly.

## [0.13.0] - Unreleased

### **⚠ ACTION REQUIRED — rebuild images, recreate containers with fresh volumes, restart the control plane**

> Run `clawker build --no-cache` in every project after updating, recreate
> containers with fresh volumes (`clawker rm --volumes`), and restart the
> control plane (`clawker controlplane down && clawker controlplane up`).
> Legacy config keys are migrated automatically with a printed notice on first
> load. Step-by-step instructions: <https://docs.clawker.dev/upgrading/v0.13>.

- **Added:** Multi-harness support. Coding-agent CLIs are pluggable **harness bundles** — a manifest, a Dockerfile template fragment, and assets. Shipped bundles resolve straight from the clawker binary; register your own per-project in `clawker.yaml` under `harnesses:` (name → bundle path). OpenAI **Codex** ships alongside Claude Code; add your own by authoring a bundle (see the harness-bundles docs page). Per-harness config lives in `clawker.yaml` under `harnesses.<name>:` (env, `post_init`/`pre_run`, managed-config strategy).
- **Added:** Harness-keyed images. Builds tag `clawker-<project>:<harness>` (the default harness also gets a `:default` alias); `clawker build -t codex` builds a specific harness, `clawker run @:codex` selects its image, and bare `@` resolves the default. Containers and images carry a harness label.
- **Added:** Language **stacks** (`build.stacks: [go, node, python, rust]`) — file-backed install definitions. Shipped stacks resolve from the clawker binary; register your own per-project in `clawker.yaml` under `stacks:` (name → definition path). Harness bundles declare the stacks they need and projects add their own; a closer layer (project registry, then bundle) shadows a shipped definition of the same name, reported in the build output.
- **Added:** `clawker stack` and `clawker harness` commands to register, list, and remove definitions in the project's `clawker.yaml` — `register <path> [--name <n>] [--force]`, `list` (with `-q`/`--json`/`--format`), and `remove <name>`. Registration points a name at a stack or bundle directory on disk; `list` shows project-registered alongside built-in entries and flags which shadow a shipped name.
- **Added:** Per-harness build overlay. `build.harnesses.<name>` takes the same `stacks`, `packages`, and `inject` primitives as the base `build` block, scoped to one harness's image — overlay stacks render after the bundle's own stacks, overlay packages install in that harness image, and overlay `inject.after_harness_install`/`before_entrypoint` apply to that harness only.
- **Changed:** Stack and harness registration is per-project in `clawker.yaml` (`stacks:` / `harnesses:`).
- **Changed:** Stack and harness names use one rule — lowercase kebab-case, up to 32 characters (`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`), and the directory name is the registered name unless `--name` overrides it.
- **Changed:** Images build from clawker's pinned Debian base. `build.image`, `build.dockerfile`, and `build.context` are removed (auto-stripped from config with a notice); custom base images and Dockerfiles are unsupported — customize with `build.stacks`, `build.packages`, `build.instructions`, and `build.inject`.
- **Changed:** Harness auth happens in-container. Host credentials are no longer copied into containers (`agent.claude_code.use_host_auth` removed); authenticate once on first run — browser flows are proxied to the host — and the login persists in the harness config volume. Managed config (settings, plugins, skills) is still staged from the host.
- **Changed:** `agent.claude_code` config moved to `harnesses.claude` (migrated automatically with a notice); `build.inject.after_claude_install` is now `after_harness_install` (the old name still works as a deprecated alias).
- **Fixed:** `clawker build --build-arg` targeting a base-image build ARG now rebuilds the base when the value changes, instead of being silently dropped when the rest of the base was unchanged. Build args the base image doesn't declare (harness-only or unknown) never trigger a base rebuild.
- **Removed:** `clawker generate` — the Claude-Code-release Dockerfiles it generated no longer exist in the bundle-composed build model.

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
