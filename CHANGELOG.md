# Changelog

All notable, user-facing changes to clawker are documented here. This is the
**curated** changelog — it intentionally covers only the handful of releases
that change the user surface, not every tech-debt or dependency bump. The
exhaustive per-commit list lives in each GitHub release's "Commits" section.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and clawker adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A release spans many merged PRs and may mix change kinds — Added, Fixed,
Changed, Removed. Each release section lists those subsections directly; the
clawker CLI's "what's new" teaser renders the section bodies verbatim as
markdown, and the release-notes workflow copies them into the GitHub release.
Link to relevant docs inline in the bullets.

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
  producing a broken checkout. [Docs](https://docs.clawker.dev/worktrees)

## [0.12.0] - 2026-06-11

### Added

- **Command aliases, with `clawker go` and `clawker wt` built in.** clawker now
  ships shortcut commands you can use right away: `clawker go <agent>` launches a
  throwaway interactive agent against the current project, and
  `clawker wt <agent> <branch>` does the same inside a git worktree for that
  branch. Manage aliases with the new `clawker alias` command (`list`, `set`,
  `delete`, `export`), and define your own subcommand shortcuts under `aliases:`
  in `clawker.yaml` — with positional `$1..$N` argument substitution — invoked
  like any built-in command. [Docs](https://docs.clawker.dev/aliases)

## [0.11.0] - 2026-06-10

### Fixed

- **Worktree containers protect the host repository.** When the workspace is a
  git worktree, the main repository's `.git/hooks` and `.git/config` are mounted
  read-only so container-side writes cannot execute code on the host. Go builds
  inside worktree containers no longer fail on VCS stamping, and starting a
  second container against an already-checked-out branch is refused instead of
  corrupting the worktree. [Docs](https://docs.clawker.dev/worktrees)
- **Worktrees track their upstream branch.** `clawker worktree add` now pulls
  remote-tracking branches with their upstream set, so `git pull`/`git push`
  work without manual `--set-upstream`.

## [0.10.3] - 2026-06-09

### Fixed

- **Expired-but-refreshable host login is forwarded.** Claude Code credentials
  copied from the host are injected even when the host access token has expired,
  as long as it is still refreshable — the container refreshes on first use
  instead of forcing a fresh `/login`. [Docs](https://docs.clawker.dev/credentials)

## [0.10.0] - 2026-06-06

### Added

- **`clawker firewall refresh`.** Live-apply egress edits made to
  `clawker.yaml` (`security.firewall.add_domains` and `security.firewall.rules`)
  into the running firewall without restarting any container. Add/update only —
  removing a rule still uses `clawker firewall remove`. [Docs](https://docs.clawker.dev/firewall)
- **Every-start container hook (`pre_run`).** The `agent.pre_run` script runs on
  every container start (not just the first), complementing the once-only
  `post_init` hook.

## [0.9.0] - 2026-05-18

### Changed

- **Monitoring stack moved to OpenSearch + Prometheus.** The bundled
  observability stack now uses OpenSearch (logs) and Prometheus (metrics) in
  place of the previous Loki/Jaeger/Grafana stack, with a preconfigured
  OpenSearch Dashboards setup applied by `clawker monitor up`. [Docs](https://docs.clawker.dev/monitoring)

## [0.8.0] - 2026-05-12

### Removed

- **Looping ("ralph") mode has been retired.** The agent loop subsystem is gone;
  drive iteration through your own workflow or Claude Code directly. A
  managed-settings `PATH` regression introduced alongside it is also fixed. [Docs](https://docs.clawker.dev/quickstart)

## [0.7.0] - 2026-04-03

### Added

- **`clawker skill` commands.** Manage Claude Code plugin skills directly from
  clawker — list, add, and remove skills for the agent's Claude Code
  installation.

## [0.6.0] - 2026-03-26

### Changed

- **Preset-based guided `init`.** `clawker init` was rewritten as a guided,
  preset-based setup flow, making first-run project configuration substantially
  faster and less error-prone. [Docs](https://docs.clawker.dev/quickstart)

## [0.5.0] - 2026-03-20

### Added

- **Global egress firewall stack.** A shared Envoy + custom CoreDNS + eBPF
  egress-enforcement stack governs outbound traffic from every agent container,
  with per-project allow rules declared in `clawker.yaml` under
  `security.firewall`. [Docs](https://docs.clawker.dev/firewall)

## [0.3.0] - 2026-02-24

### Added

- **Host-path workspace mounts.** Run an agent against a live bind mount of the
  host project directory, so changes made in the container are immediately
  visible on the host (the alternative to ephemeral snapshot workspaces). [Docs](https://docs.clawker.dev/worktrees)

## [0.1.0] - 2026-02-11

### Added

- **Git worktree support.** Run an agent in an isolated git worktree keyed to a
  branch, including slashed branch names, via `clawker worktree add` and
  `clawker run --worktree`. [Docs](https://docs.clawker.dev/worktrees)
- **Git credential forwarding.** SSH agent, GPG agent, and HTTPS git
  credentials are forwarded from the host through a socket bridge, so commits
  sign and private repositories clone inside the container.

[0.12.3]: https://github.com/schmitthub/clawker/releases/tag/v0.12.3
[0.12.0]: https://github.com/schmitthub/clawker/releases/tag/v0.12.0
[0.11.0]: https://github.com/schmitthub/clawker/releases/tag/v0.11.0
[0.10.3]: https://github.com/schmitthub/clawker/releases/tag/v0.10.3
[0.10.0]: https://github.com/schmitthub/clawker/releases/tag/v0.10.0
[0.9.0]: https://github.com/schmitthub/clawker/releases/tag/v0.9.0
[0.8.0]: https://github.com/schmitthub/clawker/releases/tag/v0.8.0
[0.7.0]: https://github.com/schmitthub/clawker/releases/tag/v0.7.0
[0.6.0]: https://github.com/schmitthub/clawker/releases/tag/v0.6.0
[0.5.0]: https://github.com/schmitthub/clawker/releases/tag/v0.5.0
[0.3.0]: https://github.com/schmitthub/clawker/releases/tag/v0.3.0
[0.1.0]: https://github.com/schmitthub/clawker/releases/tag/v0.1.0
