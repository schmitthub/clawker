# Project guide

Read this file for CLI, config, package boundary, or terminal work.
The [architecture](ARCHITECTURE.md) and [design](DESIGN.md) documents define
the system. Read [REPO-STRUCTURE.md](REPO-STRUCTURE.md) for package locations
and [KEY-CONCEPTS.md](KEY-CONCEPTS.md) for types. Package `AGENTS.md` files
provide API references; check current package source for exact signatures.
Code and command paths below start at the repository root.

## CLI Commands

See `docs/cli-reference/` for auto-generated command reference.

**Top-level shortcuts**: `init`, `monitor *`, `version`, plus Docker-CLI-style container/image verbs each aliasing the matching subcommand (`build`, `create`, `run`, `start`, `stop`, `restart`, `kill`, `pause`, `unpause`, `rm`, `rmi`, `ps`, `attach`, `exec`, `logs`, `cp`, `rename`, `stats`, `top`, `wait`)
**Management**: `alias *`, `auth *`, `bundle *`, `harness *`, `prompt *`, `stack *`, `container *`, `volume *`, `network *`, `image *`, `project *`, `worktree *`, `firewall *`, `controlplane *`, `settings *`, `plugin *` (alias `skill`)

## Configuration

> Always use `Config` interface accessors — never hardcode filenames or env var names. See `internal/config/AGENTS.md`.

### Project Config (`clawker.yaml`)

```yaml
build:
  harness: "claude"
  packages: ["git", "ripgrep"]
  instructions: { env: {}, copy: [], root_run: [], user_run: [] }
  inject: { after_from: [], after_packages: [] }
agent: { env_file: [], from_env: [], env: {}, post_init: "", pre_run: "" }
workspace: { default_mode: "bind" }
security: { firewall: { add_domains: [], rules: [] }, docker_socket: false, git_credentials: { forward_https: true, forward_ssh: true, forward_gpg: true, copy_git_config: true } }
```

## Design Decisions

1. Firewall enabled, Docker socket disabled by default
2. Top-level shortcuts (`run`, `start`, `stop`, `ps`, ...) alias their matching `container`/`image` subcommand (Docker CLI pattern)
3. Hierarchical naming: `clawker.project.agent`; labels (`dev.clawker.*`) authoritative for filtering
4. Send data, status, success, and next steps to stdout; warnings and errors to stderr. With `--format`, send formatted data to stdout and status/progress to stderr. Live output uses the TUI rules.
5. Project registry replaces directory walking for resolution
6. Global-scope agents (no project) → 2-segment names (`clawker.agent`); the `dev.clawker.project` label is intentionally absent (not present as an empty string), matching the 2-segment name shape
7. Factory is a pure struct with closure fields; constructor in `internal/cmd/factory/`. Commands use `NewCmd(f, runF)` pattern
8. Factory noun principle: fields return nouns, not verbs (`f.HostProxy().EnsureRunning()` not `f.EnsureHostProxy()`)
9. Package boundary: config file I/O + config-path helpers → `internal/config`; project identity/CRUD + project-root resolution (registry via `internal/storage`) → `internal/project`. `config` receives the resolved root as a primitive anchor (`WithProjectRoot(root)`); it does not depend on `internal/project`

## Important Gotchas

* `os.Exit()` does NOT run deferred functions — restore terminal state explicitly
* Raw terminal mode: Ctrl+C goes to container, not as SIGINT
* Don't wait for stdin goroutine on container exit (may block on Read)
* Docker hijacked connections need cleanup of both read and write sides
* Terminal visual state must be reset separately from termios mode — `term.Restore()` sends escape sequences before restoring raw/cooked mode
* Docker Desktop SDK `HostConfig.Mounts` behaves differently from `Binds` for Unix sockets on macOS
* `.clawkerlocal/` may exist during local development — check before defaults (see: `make localenv`)
