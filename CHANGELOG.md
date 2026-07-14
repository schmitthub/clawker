# Changelog

All notable, user-facing changes to clawker are documented here. This is the
**curated** changelog — it intentionally covers only the handful of releases
that change the user surface, not every tech-debt or dependency bump. The
exhaustive per-commit list lives in each GitHub release's "Commits" section.

The format follows Keep a Changelog, and clawker adheres to Semantic Versioning.

A release spans many merged PRs and may mix change kinds — Added, Fixed,
Changed, Removed. Each release section lists those subsections directly.

## [0.13.0] - Unreleased

- **Added:** Multi-harness support — clawker runs Claude Code, OpenAI Codex, or any harness you supply. Images are per-project **and** per-harness: pick the harness at build time with `clawker build -t <harness>` (the image tag is the harness name) and at run time with the `@:<harness>` selector. Per-harness project config lives under the project-root `harnesses.<name>:` map, layered on the harness-agnostic `agent.*` base.
- **Added:** `build.harness` — set the default harness. A bare `clawker build` builds it and a bare `@` runs it; set it in your user-level `clawker.yaml` to make any harness the default across every project, or per project in `.clawker.yaml`. Unset, the default remains the built-in `claude`.
- **Added:** Bundles — install harnesses, stacks, and monitoring extensions distributed as a git repository or a local directory. Declare sources under a `bundles:` key in `clawker.yaml` (any config layer) and manage them with `clawker bundle install | list | prune | remove | update | validate`. Bundled components are addressed by their qualified `namespace.bundle.component` name.
- **Added:** `clawker bundle prune` — remove bundle cache entries no declaration addresses. The cache is garbage-collected against your declarations across every registered project: installing or updating a bundle cleans up that bundle's stranded slots automatically (an edited ref or swapped url no longer leaves the old copy behind forever), and `bundle prune` sweeps the whole cache. Prune also flags any bundle identity cached from two or more different repositories across projects, so a look-alike source doesn't go unnoticed.
- **Added:** Loose local extensions — drop a harness, stack, or monitoring-extension directory into `.clawker/{harnesses,stacks,monitoring}/<name>/` in a project (or the same path under your user config directory) and it is available immediately by name, with no install step. The built-in `claude`, `codex`, `node`, `go`, `python`, `rust`, and `claude-code` components remain available by bare name.
- **Added:** Component inventory commands — `clawker harness list`, `clawker stack list`, and `clawker monitor extensions` list every resolvable component of their type with its provenance: a component that shadows a farther tier is marked, and a bundled component names its owning bundle. `clawker bundle list` shows the bundles themselves, linking each declaration to its install state.
- **Added:** `monitor.extensions` — select which monitoring extensions a project contributes to the observability stack. `clawker monitor up` seeds the selected extensions when it brings the stack up. There is no default selection — every extension, including the built-in `claude-code` one, is an explicit opt-in.
- **Added:** `clawker monitor reload` — apply a `monitor.extensions` edit to a running monitoring stack by re-seeding the project's selection, re-rendering the collector config, and recreating the collector. `monitor up` is bring-up only — a stack that is already running is left untouched with a pointer at `monitor reload`. `monitor init` scaffolds the base stack only, with no extensions.
- **Changed:** Two projects seeding same-named loose monitoring extensions with different content is now a hard error naming both projects, instead of a warned overwrite. Rename one extension or reset the stack with `clawker monitor down --volumes`.
- **Changed:** The bundle cache is keyed by the declared source value in its entirety (url, ref/sha pin, subdirectory) — a declaration can never resolve content fetched from a different value. Editing a declaration fetches into a fresh cache slot, installing never collides with an existing cache entry, and two projects declaring the same bundle from different URLs or pins coexist on one host cache.
- **Changed:** The `agent.claude_code` block moved to the project-root harnesses map as `harnesses.claude`. Existing config is moved automatically with a notice on first load.
- **Changed:** Host credentials are no longer copied into containers (the `agent.claude_code.use_host_auth` key is gone). Authenticate once inside the container — browser OAuth flows are proxied to your host browser — and the login persists in the harness config volume.
- **Removed:** `build.image`, `build.dockerfile`, and `build.context` — custom base images and user-supplied Dockerfiles no longer apply; every image builds from clawker's pinned base. Customize the image with `build.stacks`, `build.packages`, `build.instructions`, and `build.inject`. Existing keys are removed automatically with a notice on first load.
- **Fixed:** `clawker build --build-arg` targeting a base-image build ARG now rebuilds the base when the value changes, instead of being silently dropped when the rest of the base was unchanged. Build args the base image doesn't declare (harness-only or unknown) never trigger a base rebuild.

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
