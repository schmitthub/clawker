# Master Implementation Plan: Generic socket bridges for harnesses

Authority: `mem:initiative_generic_socket_bridge/PRD` (24 ratified decisions).
This plan is color-by-numbers. If a task requires a decision the PRD and this
plan do not answer, **STOP work and ask the user**. Do not improvise, do not
redesign, do not expand scope.

## How to work this plan (every agent, every task)

1. Work the tasks strictly in order, one at a time; finish a task before
   starting the next.
2. TDD: write the task's tests first, then the code, then make them pass.
   A task is complete only when its listed test commands pass.
3. NEVER run `go test ./...` (tears down the host CP). Use the task's listed
   test commands or `make test`.
4. Commit per finished task on one feature branch for the whole initiative
   (GPG-signed; never chain `git add && git commit`; never push main).
5. **Per-task exit gate:** before moving on you MUST edit THIS file
   (`.serena/memories/initiative_generic_socket_bridge/plan.md`): set the
   task's status to DONE in the status table and append key learnings under
   the task's Learnings heading. Keep the plan current-state: fold changes
   into the task text itself, never as addendums or historical notes.
6. Review happens once, after all tasks are done — do not pause for
   approval between tasks. When everything is finished, summarize what
   shipped per task for the reviewer.

## Status

| # | Task | Status |
|---|------|--------|
| 1 | Manifest contract (`sockets:` in harness.yaml) | DONE |
| 2 | CLI database package (`internal/db`) with grant store | DONE |
| 3 | Path + listener-identity utils | DONE |
| 4 | `clawker sockets` command group | DONE |
| 5 | Pre-start authorization + `--approve-grants` | DONE |
| 6 | Bridge generalization | DONE |
| 7 | Readiness barrier | DONE |
| 8 | Integration tests | DONE |
| 9 | Docs, schemas, memories | DONE |

---

## Task 1 — Manifest contract

**Goal:** harness.yaml gains a `sockets:` list.

**Where:**
- `internal/config/schema.go` — the component manifest type is
  `config.Manifest`. Add:
  ```go
  // HarnessSocket declares one host socket bridge (PRD decisions 12-24).
  type HarnessSocket struct {
      Source    string                 `yaml:"source"`   // host socket expression ($VAR/${VAR}/~ only)
      Target    string                 `yaml:"target"`   // absolute container destination
      Purpose   string                 `yaml:"purpose"`  // user-facing reason, shown in the prompt
      Optional  bool                   `yaml:"optional,omitempty"`  // harness runs without it (decision 17)
      Container HarnessSocketContainer `yaml:"container,omitempty"` // socket file perms (decision 23)
  }

  // HarnessSocketContainer sets the in-container socket file's group and
  // mode. The group must already exist in the image with the agent user as
  // a member — the harness Dockerfile fragment provisions it; clawker
  // creates no users or groups (decision 23).
  type HarnessSocketContainer struct {
      Group string `yaml:"group,omitempty"` // default: agent user's primary group
      Mode  string `yaml:"mode,omitempty"`  // octal string, default "0600"
  }
  ```
  and `Sockets []HarnessSocket \`yaml:"sockets,omitempty"\`` on `Manifest`.
  (Find `Manifest` with serena; it lives beside the other manifest shapes —
  `bundle` imports config for manifest shapes only.)
- `internal/consts` — new `BannedSocketPaths` slice: the fixed socket-path
  ban list; the Docker daemon socket path is its first member. Call sites
  reference the slice, never the path string.
- `internal/bundler/bundle.go` `LoadBundle` — add `validateSockets(name, m.Sockets)`
  beside `validateEgressFloor` (front-door validation law). Rules:
  - `Source`, `Target`, `Purpose` required and non-empty.
  - `Target` absolute.
  - `Source` uses only `$VAR`/`${VAR}`/leading-`~` (reject backticks, `$(`,
    `*`, `?`).
  - Duplicate `Target` in one manifest = error.
  - Literal `Source` matching `consts.BannedSocketPaths` = error naming the
    banned path; for the Docker daemon socket the error also names
    `security.docker_socket` (decision 21). Applies regardless of
    `Optional`. This static check catches literal declarations; the
    resolution-time check in task 5 is the load-bearing gate.
  - `Container.Mode` empty or a valid octal mode `0000`–`0777`;
    `Container.Group` empty or a valid group name. No other validation —
    group existence is checked at runtime by the forwarder (decision 23).
- No `container.owner`, direction, or access-mode fields exist; the harness
  declares nothing about the host listener (decision 24). Do not add
  provider-specific fields.
- `internal/bundler.LoadHarness` is the canonical runtime reader. After raw
  manifest validation, it applies the existing `config.ExpandHostPath`
  semantics to each socket source and returns those absolute values in the
  typed manifest. `LoadBundle` keeps raw source values for static validation
  and build use.

**Tests (isolated):** extend `internal/bundler/bundle_test.go` table style —
valid manifest with sockets (including optional + container block); missing
purpose; relative target; command substitution in source; duplicate target;
banned path as source (rejected even with `optional: true`); invalid mode
string; `optional: true` parses; omitted optional defaults false.
Run: `go test ./internal/bundler/... ./internal/config/...`

### Learnings (task 1)

- The harness manifest shape is in `internal/config/harness_schema.go`; `config.Manifest` remains the single owner of socket declaration fields.
- Socket validation runs in `bundler.LoadBundle` with the other front-door checks. The banned-path list uses the Docker socket path constant as its first item.
- Runtime harness loads use the existing `config.ExpandHostPath` function for
  socket sources. Command code receives expanded source values in the typed
  manifest and does not implement a second expansion path.

---

## Task 2 — Grant store package

**Goal:** new `internal/db` package: the CLI-owned SQLite database, holding
the connection machinery plus the socket-grant store. Named `db` (generic)
because it is the CLI's only database home; future CLI-side tables land
here too.

**Where:** new package `internal/db/` with `db.go` (connection struct,
`Open(path, *logger.Logger) (*DB, error)`, migration via `PRAGMA
user_version`, `Close`), `grants.go` (grant row type + verbs), `consts.go`,
`grants_test.go`, `mocks/` (moq via `//go:generate`, matching
`internal/socketbridge/mocks` pattern).

**Schema ownership (no drift):** `internal/config` owns the declaration
shape (`config.HarnessSocket`); `internal/db` imports config and its grant
API takes the declaration verbatim — the row extends it with identity and
bookkeeping fields and stores `Purpose` from the declaration. Nothing
re-declares declaration fields.

**Facts to follow:**
- Driver: `modernc.org/sqlite` (already in go.mod). Precedent for open/init
  patterns: `controlplane/agent/registry_sqlite.go`. Do NOT touch that file
  or reuse its db — this is a NEW host-only db (PRD: WAL safe because never
  bind-mounted).
- DB file: `<consts.StateDir()>/socket-grants.db` — add the filename and a
  subpath helper to `internal/consts` (no hardcoded strings; follow existing
  `StateDir()` at `internal/consts/consts.go`).
- Schema (decisions 14, 22, 24):
  ```sql
  CREATE TABLE grants (
      id           INTEGER PRIMARY KEY AUTOINCREMENT,  -- THE grant ID: shown by list, passed to revoke
      harness_path TEXT NOT NULL,  -- realpath of harness source dir
      harness_name TEXT NOT NULL,  -- display only
      host_path    TEXT NOT NULL,  -- resolved absolute host socket path
      status       TEXT NOT NULL CHECK (status IN ('allow','deny')),  -- stored answer (decision 22)
      host_uid     INTEGER NOT NULL,  -- observed listener uid at approval (decision 24)
      host_gid     INTEGER NOT NULL,  -- observed listener gid at approval
      host_owner   TEXT NOT NULL,     -- owner name at approval, display only
      host_group   TEXT NOT NULL,     -- group name at approval, display only
      purpose      TEXT NOT NULL,     -- declaration's purpose text, display only
      granted_at   TEXT NOT NULL,     -- RFC3339
      UNIQUE (harness_path, host_path)
  );
  ```
  `PRAGMA user_version = 1`. WAL + busy_timeout on open.
- API split (no god struct): `db.go`'s `*DB` is connection machinery ONLY —
  Open, migrate, Close, zero table methods, forever. Store verbs live on a
  separate store type in `grants.go`, constructed by
  `NewSocketGrantStore(*DB)`. A future table = a new store file; `*DB` and
  its API never grow. Verbs:
  ```go
  type SocketGrant struct {
      ID          int64  // auto-increment PK; the user-facing grant ID
      HarnessPath string // principal: realpath of harness source dir
      HarnessName string // display only
      HostPath    string // resolved absolute host socket path
      Status      string // consts in this package: GrantAllow | GrantDeny
      Identity    socketbridge.ListenerIdentity // observed at approval
      Purpose     string // from config.HarnessSocket.Purpose, display only
      GrantedAt   time.Time
  }

  LookupSocketGrant(harnessPath, hostPath string) (*SocketGrant, error)
      // nil, nil when no row exists — the caller prompts
  GrantSocket(harnessPath, harnessName, hostPath string, decl config.HarnessSocket, ident socketbridge.ListenerIdentity) error
      // upsert on UNIQUE pair with status=allow; logs the event
  DenySocket(harnessPath, harnessName, hostPath string, decl config.HarnessSocket, ident socketbridge.ListenerIdentity) error
      // same upsert with status=deny; shares the internal write path
  RevokeSocket(id int64) error
  RevokeHarnessSockets(harnessPath string) error
  RevokeAllSockets() error
  ListSocketGrants() ([]SocketGrant, error)
  PruneHarnessSockets(harnessPath string, declaredHostPaths []string) error // decision 20 autoprune
  ```
  `socketbridge.ListenerIdentity` is declared in
  `internal/socketbridge/identity.go` by this task. Task 3 adds the platform
  probes and reuses this type.
- Grant ID: the auto-increment integer PK, assigned by the db. `sockets
  list` shows it; `RevokeSocket` deletes by `WHERE id = ?`. AUTOINCREMENT so
  a revoked ID is never reused.
- Command-layer seam: declare a `SocketGrantStore` interface in the package
  covering the verbs above; moq it. Prod passes the
  `NewSocketGrantStore(*DB)` concrete.
- `GrantSocket`/`DenySocket` know nothing about prompts or flags: they
  write the row, log the event (principal, resolved path, identity, status,
  time) via an injected `*logger.Logger` (constructor-injected per
  code-style; tests use `logger.Nop()`), and return.

**Tests (isolated):** tmp-dir db; grant/lookup round-trip (status=allow,
identity persisted); deny/lookup round-trip; lookup with no row → nil, nil;
grant-then-deny same pair flips status; upsert on same pair updates
identity; revoke by id; revoke harness/all; prune keeps declared, removes
undeclared, never touches other harness_path rows; two concurrent processes
(two Opens) don't lose writes; revoked id not reused.
Run: `go test ./internal/db/...`

### Learnings (task 2)

- `internal/db.DB` owns only the host-side SQLite connection, migrations, and close operation. `NewSocketGrantStore(*DB)` composes the table-specific grant API without adding table methods to the connection type.
- Each process database handle uses one connection; WAL and the busy timeout coordinate concurrent CLI writers.
- `socketbridge.ListenerIdentity` is the shared declaration type. This keeps the database API in the planned form and keeps `socketbridge` independent from `db`.
- The installed `moq` v0.7.1 binary was built with Go 1.27 and failed to load this Go 1.26.6 module. Running the same pinned version with Go 1.26.6 generated the mock correctly.

---

## Task 3 — Path + listener-identity utils

**Goal:** two small utils the command layer calls per declared source.

**A. `internal/cmdutil/paths.go`** (+ `paths_test.go`):
```go
// ResolveHostPath resolves an expanded absolute host path to its real
// filesystem target. The path must exist.
func ResolveHostPath(path string) (string, error)
```
- Expansion belongs to the canonical `bundler.LoadHarness` reader through
  `config.ExpandHostPath`. This includes `$VAR`, `${VAR}`,
  `${VAR:-fallback}`, and leading `~` semantics.
- `ResolveHostPath` rejects an empty or non-absolute value, then uses
  `filepath.EvalSymlinks` to return the real path. Missing path = error naming
  the path.

**B. `internal/socketbridge/peercred_linux.go` / `peercred_darwin.go`**
(+ test):
```go
// ListenerIdentity is the peer identity of a Unix socket listener.
type ListenerIdentity struct {
    UID, GID     int
    Owner, Group string // names resolved for display; empty if unresolvable
}

// ReadListenerIdentity dials the Unix socket at path, reads the listener's
// peer credentials, resolves names, and closes the probe connection.
func ReadListenerIdentity(path string) (ListenerIdentity, error)
```
- linux: `golang.org/x/sys/unix.GetsockoptUcred` (`SO_PEERCRED`); darwin:
  `unix.GetsockoptXucred` (`LOCAL_PEERCRED`). Names via `os/user`
  `LookupId`/`LookupGroupId`; a failed name lookup leaves the name empty,
  never errors (numeric ids are the comparison values — decision 24).
- Dial failure (no permission, no listener) = error; callers fail closed.

Trust tier and principal are NOT part of these utils — the command layer
does both inline (task 5): trust = `prov.Tier == bundle.TierFloor`
(decision 19); principal = `ResolveHostPath(prov.Dir)`.

**Tests (isolated):** canonical harness loading expands an environment value
and `${MISSING:-~/.missing}` fallback into absolute typed values; a symlinked
absolute path resolves to realpath; an unexpanded expression and a missing
path fail with the input in the message; identity probe against an in-test
listener returns the test uid/gid; probe of a dead socket errors.
Run: `go test ./internal/cmdutil/... ./internal/socketbridge/...`

### Learnings (task 3)

- `bundler.LoadHarness` applies the existing configuration path semantics.
  `ResolveHostPath` only accepts the expanded absolute value and resolves its
  real filesystem target. This prevents authorization code from owning a
  second configuration expansion implementation.
- `config.ExpandHostPath` rejects a complete expansion that is empty. This
  prevents an unset source such as `$MISSING` from becoming the current
  directory. A missing variable with a remaining absolute path still uses the
  existing configuration semantics.
- The shared listener probe owns connection and name-resolution handling. Linux reads `SO_PEERCRED`; Darwin reads `LOCAL_PEERCRED` and uses the first returned group as the peer group.
- The Darwin socketbridge package compiles for arm64 with the platform implementation.

---

## Task 4 — `clawker sockets` command group

**Goal:** `sockets list`, `sockets info <id>`,
`sockets revoke <id> | --harness <name> | --all`, `sockets prune [-a]`.
NO `sockets grant` (decision 18). All output through `ios.ColorScheme()` +
`f.TUI` per `.claude/rules/code-style.md` — no raw tabwriter, no bare
prints.

**Where:**
- New `internal/cmd/sockets/` package: `sockets.go` (NewCmdSockets(f)) +
  subcommand files. Copy the structure of `internal/cmd/firewall/` (closest
  sibling: management group over a store).
- Register in `internal/cmd/root/root.go` beside
  `firewallcmd.NewCmdFirewall(f)`.
- Factory noun: add `DB func() (*db.DB, error)` to the Factory struct
  (`internal/cmd/factory/`) — lazy, sync.Once-cached like the other nouns.
  This is the CLI database's ONE permanent Factory noun; the Factory never
  gains per-table nouns. Each command's `NewCmd(f, runF)` composes the store
  in a closure on its Options struct:
  ```go
  opts.SocketGrants = func() (db.SocketGrantStore, error) {
      d, err := f.DB()
      if err != nil { return nil, err }
      return db.NewSocketGrantStore(d), nil
  }
  ```
  Options fields stay typed as the store interface, so tests inject the moq
  mock through the same field. Commands resolve in their run function
  (`NewCmd(f, runF)` pattern; `Example` field; `PersistentPreRunE` if any
  hook needed).

**Behavior:**
- `list`: table via `f.TUI.NewTable(...)` + `--format` machine output.
  Columns: ID (integer PK), HARNESS (harness_name), STATUS (allow/deny),
  PURPOSE (purpose) — nothing else. Data → stdout.
- `info <id>`: full, nicely formatted dump of the entire row — id, harness
  name, principal path (harness_path), host socket path, status, observed
  listener identity (owner:group with uid/gid), purpose, granted_at.
  gh-style detail view: `ios.ColorScheme()` semantic colors (`cs.Primary`
  for field labels, plain values), aligned key/value layout via TUI helpers
  — if no suitable generic view exists in `internal/tui`, add one there
  (generic, no command-specific logic) and use it. Supports `--format` for
  machine output. Same id completion as revoke.
- `revoke`: deletes row(s) of either status; after success print to stdout a
  short note that running containers keep their bridges until stop/restart
  (decision 16).
- Completion: wire `ValidArgsFunction` on `revoke`'s and `info`'s positional
  id arg to a completion func returning `fmt.Sprintf("%d\t%s → %s (%s)",
  g.ID, g.HarnessName, g.HostPath, g.Purpose)` per grant —
  `ShellCompDirectiveNoFileComp`, exclude ids already present in args, all
  failures degrade to no suggestions via `cobra.CompDebugln`. Follow the
  existing pattern: `internal/cmd/worktree` `shared.BranchCompletions` and
  `internal/cmd/firewall/remove`. Also `RegisterFlagCompletionFunc` for
  `--harness` on revoke (distinct harness_name values from the store).
- `prune`: for each distinct harness_path, resolve the harness through
  `bundle.NewResolver(cfg)`; if it does not resolve → delete its rows; if it
  resolves → keep only rows matching currently declared (resolved) sockets.
  `-a`/`--all`: delete every row (confirm via prompter unless `--yes`,
  mirroring `firewall prune`).
- Errors: typed returns to Main; never print errors directly.

**Tests (isolated):** command tests with `db/mocks` + iostreams
test streams — list output (4 columns only, deny rows shown), info full
dump incl. status and identity, revoke by id, revoke --all confirmation,
prune resolved/unresolved split, prune -a; completion func returns
id+description lines, filters already-typed ids, degrades to empty on store
error (mirror `worktree/remove_test.go` / `firewall/remove/remove_test.go`).
Run: `go test ./internal/cmd/sockets/...`

### Learnings (task 4)

- The Factory caches one `*db.DB` connection. Each sockets command composes `db.NewSocketGrantStore` in its Options closure, which keeps table-specific APIs out of the Factory and keeps command tests on the generated store mock.
- `tui.TUI.RenderDetails` provides the generic aligned detail view and applies the active color scheme to labels. List and machine output use the common TUI table and format helpers.
- Explicit prune groups rows by the real harness principal. It removes missing or replaced harness principals and keeps only socket declarations that resolve in the current host environment.
- Grant ID and harness completion functions use the store interface directly. Store failures produce debug completion output and no suggestions.

---

## Task 5 — Pre-start authorization + `--approve-grants`

**Goal:** the single checkpoint (decisions 15, 18–24) inside
`BootstrapServicesPreStart`.

**Where:** `internal/cmd/container/shared/container_start.go`.

**Wiring:**
- Add to `CommandOpts`: `SocketGrants func() (db.SocketGrantStore, error)` and
  `ApproveGrants bool` (the flag value), plus a `RuntimeHarness` value that
  contains the selected typed harness fields. All construction sites of
  `CommandOpts` (run/start/restart commands under `internal/cmd/container/`)
  build the store closure over the `f.DB()` Factory noun
  (`db.NewSocketGrantStore` over the handle — same shape as task 4); find
  them with serena references on `CommandOpts`.
- Command run functions are the composition roots. `start` and `restart` use
  `shared.LoadContainerHarness`, which reads the container label and calls the
  canonical `bundler.LoadHarness` reader once. `run` reuses the bundle already
  returned by container creation. Each command constructs `RuntimeHarness`
  from the loaded bundle's name, provenance, sockets, and egress values.
- Shared flag: new `internal/cmdutil/flags.go` with
  `AddApproveGrantsFlag(cmd *cobra.Command, p *bool)` registering
  `--approve-grants` (bool, default false, help: approve this start's
  declared host socket requests without prompting). Wire into `run`, `start`,
  `restart` command definitions.

**Insertion point:** in `BootstrapServicesPreStart`, after the command-supplied
runtime harness check and BEFORE the firewall block (host access must be
settled before any start work continues; not gated on
`cfg.FirewallEnabled()` — CP ≠ firewall). This function and socket
authorization do not read harness configuration.

**Logic (exact order):**
1. Use the command-supplied `RuntimeHarness` values. The existing
   `containerHarnessName` fallback to the configured default (its
   `bundler.ResolveHarnessName(cfg, "")` branch) must NOT authorize sockets:
   if the container has no harness label AND the loaded manifest declares
   sockets, fail closed with an error naming the container and the missing
   label.
2. No declared sockets → return (zero cost for every existing harness).
3. Resolve every already-expanded `decl.Source` with
   `cmdutil.ResolveHostPath`. A resolution
   error: required decl → fail closed; optional decl → skip that socket
   with a one-line ErrOut notice + log entry and continue. Check every
   resolved path against both the literal and resolved real path of each
   `consts.BannedSocketPaths` entry — a match fails the start closed for
   every tier, `Optional` included, with an error naming the banned path
   (for the Docker daemon socket, point at
   `security.docker_socket`); no prompt, no grant, `ApproveGrants` never
   consulted (decision 21).
4. Probe each surviving resolved path with
   `socketbridge.ReadListenerIdentity`. Probe error: required → fail
   closed; optional → skip + notice.
5. `provenance.Tier == bundle.TierFloor` → trusted (decision 19): collect
   the pairs directly (the observed identity rides into the registration as
   the open-time pin — decision 24) and skip the store and prompt entirely.
6. Third-party: `principal, err := cmdutil.ResolveHostPath(provenance.Dir)`
   (error → fail closed). Open store via `cmdOpts.SocketGrants()`, then
   `store.PruneHarnessSockets(principal, hostPaths)` (decision 20,
   this harness only).
7. For each (decl, hostPath, identity):
   `g, err := store.LookupSocketGrant(principal, hostPath)` (decision 22 —
   a stored row decides, no prompt):
   - `g.Status == allow` and identity matches (uid+gid) → collect the
     pair; no prompt, no output.
   - `g.Status == allow` and identity differs → drift (decision 24): run
     the four-answer prompt in its changed-identity variant (shows old and
     new identity); answers behave as below, `always`/`never` upsert the
     new identity.
   - `g.Status == deny` → required: fail closed with an error naming the
     grant ID and `sockets revoke <id>` as the remedy; optional: silent
     skip — log only, NO notice, NO prompt. `ApproveGrants` never
     overrides a stored deny.
   - no row + `cmdOpts.ApproveGrants` → acts as `always`:
     `store.GrantSocket(...)`, print the approved resolved path, collect
     the pair.
   - no row + interactive → four-answer letter prompt (PRD "Prompt"
     section): header lines show resolved path only (never the
     expression), the target, the observed listener identity, and
     `decl.Purpose` under a `Purpose:` label (harness-authored,
     untrusted), then the firewall-bypass sentence and
     `Allow this socket bridge? [a]lways / [y]es / [n]o / ne[v]er:`.
     Prompter returns the raw answer (single letter or full word,
     case-insensitive; empty re-asks; no default) — the command layer maps
     it: `always` → `GrantSocket` + collect; `yes` → collect, store
     nothing; `no` → store nothing, required fails closed (both remedies)
     / optional skips with notice; `never` → `DenySocket`, then required
     fails closed naming the new grant ID / optional skips silently.
     Static-interactive scenario via `internal/prompter`.
   - no row + non-promptable without flag: required → fail closed, error
     text offers both remedies (`--approve-grants` and an interactive
     start); optional → skip with notice, nothing stored.
8. Return the active pairs for post-start: extend
   `BootstrapServicesPreStart`'s signature to return
   `([]socketbridge.BridgedSocket, error)` and update all callers; do not
   add mutable fields to CommandOpts. Each `BridgedSocket` carries
   `{HostPath, Target, Identity, Group, Mode}` (struct defined in task 6;
   Group/Mode from `decl.Container`).

**Restart flows:** both restart paths funnel through
pre-start/ContainerStart (PRD "Current code facts"); no restart-specific
logic. Verify by running the existing shared tests.

**Tests (isolated):** unit tests use typed `RuntimeHarness` fixtures, real
Unix listeners, real symbolic links, the standard test environment, and db
mocks: builtin skips store; third-party no row + flag → granted; no row noninteractive no
flag → fail closed with both remedies in the error; stored allow +
matching identity → pair collected, no prompt; stored allow + changed
identity → changed-identity prompt; stored deny + required → fail closed
naming grant ID and `sockets revoke <id>`, flag does not override; stored
deny + optional → silent skip, no notice, no prompt; answer `never` → deny
row written then required fails closed; answer `yes` → pair collected, no
row written; empty answer re-asks; autoprune called with this principal
only; missing harness label + declared sockets → fail closed; banned path
→ fail closed before prompt even when optional, all tiers, including a
source alias that resolves to a banned path; optional
unresolvable source → skip + notice; optional approved → identical to
required.
Run: `go test ./internal/cmd/container/... ./internal/cmdutil/...`

### Learnings (task 5)

- Socket authorization runs after explicit harness resolution and before firewall setup. A container without its harness label cannot use socket declarations from the configured fallback harness.
- Built-in harnesses use their trusted tier and do not open the grant store. Third-party harnesses use the resolved harness directory as the principal, prune only that principal's declared paths, and compare listener identity by numeric UID and GID.
- The shared prompt uses one buffered reader for repeated input. This keeps piped answers available when an empty or invalid answer causes another prompt.
- Run, start, and restart share the approval flag and compose the socket-grant store over the Factory DB closure. Pre-start returns the active socket set for task 6 without mutable command state.
- Run reuses the bundle returned by container creation. Start and restart load
  the container's harness once with the canonical reader. Each command creates
  the `RuntimeHarness` literal, and lower start and authorization functions do
  not read or expand harness configuration.
- The command-level start test uses a real loose harness file with
  `${CLAWKER_TEST_SOCKET_ROOT:-~/.missing}/some.sock`, real Unix listeners,
  symbolic-link source paths, the command constructor, and the real SQLite
  grant store. It proves that output, the grant row, and the bridge input all
  use the resolved real path. An environment change makes the old grant stale
  and requires approval for the new real path.
- `attachAndStart` and the multi-container start helper each take one options
  value with `context.Context`. This removes the positional parameter group
  without changing start order.
- Runtime banned-path checks resolve the banned entries too. This keeps the
  Docker socket banned when a host, such as Docker Desktop, exposes the fixed
  path as a symbolic link to its user socket.

---

## Task 6 — Bridge generalization

**Goal:** bridge carries arbitrary approved sockets with identity pins and
container-side perms; SSH/GPG unchanged.

**Where:** `internal/socketbridge/` + `internal/cmd/bridge/bridge.go` +
`internal/hostproxy/internals/cmd/clawker-socket-server/main.go`.

**Changes:**
1. `SocketBridgeManager.EnsureBridge(containerID string, gpgEnabled bool)` →
   `EnsureBridge(opts EnsureBridgeOpts)` where
   ```go
   type EnsureBridgeOpts struct {
       ContainerID string
       GPGEnabled  bool
       Sockets     []BridgedSocket // active, from pre-start
   }
   type BridgedSocket struct {
       HostPath string           // resolved absolute host socket path
       Target   string           // container destination
       Identity ListenerIdentity // open-time peer-cred pin (decision 24)
       Group    string           // container socket file group ("" = default)
       Mode     string           // container socket file mode ("" = 0600)
   }
   ```
   Regenerate mocks (`cd internal/socketbridge && go generate ./...`).
   Update the one production caller `startSocketBridge`
   (container_start.go — receives the pre-start pairs via
   `BootstrapServicesPostStart`, whose signature gains the slice) and the
   manager tests.
2. Registration delivery daemon-ward: `startBridge` passes
   `--container/--pid-file/--gpg` argv today. Add `--sockets-file <path>`:
   the manager writes a JSON file (0600, under `cfg.BridgesSubdir()`) of
   `{host_path, target, uid, gid, group, mode}` entries before spawn — the
   JSON serialization of `BridgedSocket`. Empty `group` and `mode` are
   omitted and default in the forwarder. The daemon (`clawker bridge serve`,
   `internal/cmd/bridge/bridge.go`) reads it at startup. Never argv for the
   entries themselves. When the manager starts this daemon, it uses the
   `consts.EnvExecutable` override before `os.Executable`, matching the host
   proxy and the e2e harness's built-CLI path.
3. Container-side forwarder env: the host bridge launches the forwarder via
   `docker exec -i <id> /usr/local/bin/clawker-socket-server`
   (`internal/socketbridge/bridge.go` ~line 121). Change to
   `docker exec -i -e CLAWKER_REMOTE_SOCKETS=<json> …` — per-exec env
   overrides the create-time value (start-time delivery per PRD). JSON
   entries: existing `{path,type}` for ssh/gpg PLUS
   `{path:<target>, type:"bridged", group:<group>, mode:<mode>}` for
   generic sockets.
4. Forwarder (`clawker-socket-server/main.go` — stdlib-only TRIPWIRE, keep
   it dependency-free): it starts with root only to open its log, restores the
   configured agent user's primary and supplementary groups, then drops its
   GID and UID before socket or GPG setup. For type
   "bridged", listen on path, then apply the entry's group and mode —
   resolve group name via `os/user.LookupGroup`, `os.Chown(path, -1, gid)`,
   `os.Chmod(path, mode)`; empty group/mode → leave owner-default and
   `0600`. Any failure (unknown group, chown refused, unwritable parent
   dir) → ERROR frame + exit non-zero: the socket never reports ready and
   the readiness barrier stops the start (decision 23 — provisioning is
   the harness Dockerfile fragment's job; clawker creates nothing). OPEN
   uses the path as the socket identifier, as for ssh/gpg.
5. Host-side open: `resolveHostSocket(socketType)` gains a lookup: the OPEN
   message for a bridged socket carries the container target path; the
   daemon maps target → its sockets-file entry. Unknown id → error (the
   container can open only supplied registrations). After
   `net.Dial("unix", hostPath)` succeeds, verify the listener's peer
   credentials against the entry's uid/gid (`SO_PEERCRED` linux /
   `LOCAL_PEERCRED` darwin — reuse the task 3 platform code). Mismatch →
   close, log, ERROR frame. ssh/gpg behavior untouched.
6. Log connection open/close events per bridged socket (metadata only) via
   the daemon's existing logger.

**Do NOT:** change ssh/gpg project-settings gating, the wire protocol frame
format, or the PID-file lifecycle.

**Tests (isolated):** existing socketbridge tests still pass; new: sockets
file round-trip incl. identity; OPEN with unknown target rejected; bridged
OPEN maps to approved path; peer-cred mismatch at open → ERROR + logged;
forwarder applies group/mode, unknown group → ERROR; EnsureBridgeOpts
idempotency.
Run: `go test ./internal/socketbridge/... ./internal/cmd/bridge/...`
Task 6 may require rebuilding the embedded socket-server asset — check the
Makefile target that embeds `clawker-socket-server` and the CLAUDE.md
"make clawker" gate before committing.

### Learnings (task 6)

- The manager writes one owner-only registration file per container. Its flat
  JSON entries contain the host path, target, numeric identity, group, and
  mode; empty permission fields are omitted.
- The daemon reads the container's create-time socket environment, keeps the
  enabled SSH/GPG entries, and adds generic entries to the per-exec
  environment. This preserves the existing credential-lane settings while it
  supplies generic registrations at start time.
- A generic OPEN uses the container target as its opaque registration ID. The
  host maps it to the approved host path and checks peer credentials on the
  established Unix connection before it records the stream.
- The manager honors the common CLI executable override when it starts a
  bridge daemon. A Go e2e test process therefore starts the built clawker CLI,
  not a recursive copy of its test binary.
- The bridge starts the socket server as root only for log setup. The server
  restores the configured agent user's primary and supplementary groups, then
  drops its GID and UID before socket or GPG setup. Docker exec with `--user`
  is not sufficient because it omits supplementary image groups.
- The container forwarder applies a declared group and mode after it listens.
  It resolves these values before it creates the listener, so an unknown group
  cannot expose a transient target to the file-based readiness check. Setup
  errors send an ERROR frame before READY, and generic connection open and
  close events carry metadata in the daemon log.
- The socket server is embedded as Go source, not as a built asset, so task 6
  required no `make clawker` rebuild.

---

## Task 7 — Readiness barrier

**Goal:** the user CMD must not run before every active bridge socket
exists in the container (PRD "Post-start activation and readiness").

**Design:** mirror the pre-run hook pattern.
1. New hook const beside `consts.HookPreRun`: `HookSocketsWait`
   (`internal/consts/consts.go`).
2. In pre-start (task 5 site), after authorization, always inject a
   sockets-wait script via the existing `InjectHookScript` mechanism
   (`internal/cmd/container/shared` — same call shape as the pre-run
   injection at the bottom of `BootstrapServicesPreStart`): a no-op wrapper
   when no active sockets; otherwise a sh loop that polls `[ -S <target> ]`
   for every active bridged target passed from pre-start (skipped optional
   sockets are excluded) with a total timeout (60s), exiting non-zero on
   timeout with the missing path on stdout. Always overwrite (no
   staleness), exactly like pre-run.
3. New boot step in `controlplane/agent/boot_steps.go`: `socketsWaitStep()`
   ShellStep running the injected script, `ExitOnNonZero: true`. Prepend to
   `BootPlan`'s head — the `bootPlanPost` invariant (pre_run + agent-ready
   stay last, pinned by `TestBootPlan_PreRunShape`) must hold. Step order:
   docker-socket, sockets-wait, pre_run, agent-ready.
4. Failure semantics fall out for free: step fails → agent-ready never
   dispatches → clawkerd never releases the CMD → container start is
   incomplete and the CLI surfaces the boot-step failure. CP code MUST NOT
   panic/Fatal/Exit (CP security rules — read the CLAUDE.md
   critical_clarification before touching anything under `controlplane/`).
5. CP stays manifest-free: it only runs the injected script; the CLI is the
   only reader of harness manifests.

**Tests (isolated):** boot-plan shape test updated (order + tail invariant);
script generation unit test (no sockets → no-op; N sockets → N probes;
timeout exit code); existing executor tests pass.
Run: `go test ./controlplane/agent/... ./internal/cmd/container/...`

### Learnings (task 7)

- Pre-start always overwrites the sockets-wait hook after authorization. An
  empty active set produces the standard no-op wrapper, so a later start
  cannot run stale socket targets.
- The generated script quotes each container target, probes each active socket
  once per loop, and uses one 60-second timeout for the full set. On timeout it
  prints the first missing target to stdout and exits with status 1.
- The boot plan order is docker-socket, sockets-wait, pre-run, and agent-ready.
  The existing pre-run and agent-ready tail stays terminal and unchanged.
- Readiness failure uses the existing fatal `ShellStep` contract. The control
  plane only dispatches the injected script and gains no panic or process-exit
  path.
- The Docker e2e gate uses the embedded control-plane executable. Changes to
  the boot plan require `make cp-binary` before that host gate; otherwise the
  container runs the previous boot plan even when the source tests pass.

---

## Task 8 — Integration tests

**Goal:** end-to-end journey coverage (PRD acceptance criteria 1–27) on the
real Docker path.

**Where:** `test/e2e/` — follow `feedback_integration_tests_drive_real_journey`:
drive real commands via the existing harness (`h.Run()` only). These run on
the HOST, not inside a clawker container (never runnable via `go test ./...`
in-container).

**Fixture:** a loose test harness under the test project's
`.clawker/harnesses/sockettest/` declaring one socket whose source points at
a test-created Unix socket in a temp dir (a tiny in-test listener). Loose
dir = third-party tier = full prompt/grant path.

**Journeys (one test each, PRD criteria in brackets):**
1. create does not prompt/activate [1]; first noninteractive start without
   flag fails closed printing both remedies [6].
2. start with `--approve-grants` → grant written with observed identity,
   container runs, socket usable in container (exec + probe) [2,3,12].
3. second start → no prompt [4]; restart re-establishes bridge [11].
4. `sockets list` shows the grant; `sockets revoke` → next start requires
   approval again [7]; revoke prints running-container note [16].
5. drift: move the host socket (new resolved path) → start fails
   closed/pending approval again [8,22].
6. shadow: loose harness named `claude` with sockets → prompts (no builtin
   trust) [9]; builtin harness starts never touch the grant db [19].
7. bridge failure: point grant at a since-deleted socket → CMD never runs,
   no orphan bridge process/PID file [13].
8. `sockets prune` after deleting the loose harness dir removes its rows.
9. harness declaring a banned socket path → start fails closed before any
   prompt; no grant row written [23].
10. optional-socket harness: answer `no` → container runs without the
    bridge, no row, notice printed; answer `always` → bridge active and
    waited on [24].
11. answer `never` → deny row stored; next required start fails closed
    naming the grant ID without prompting; `--approve-grants` does not
    override; `sockets revoke <id>` → prompt returns [25].
12. container perms: fixture harness with a Dockerfile fragment creating a
    group + adding the agent user; declared `container.group`/`mode` →
    socket in container has that group and mode; a variant declaring a
    group the image lacks → CMD never runs [26].

Identity drift and open-time peer-cred mismatch [20,27] need a listener
under a different uid, which the unprivileged test host cannot create —
they are covered by the task 5/6 unit tests; log that as the reason here,
not silent omission.

Run: `go test ./test/e2e/... -run TestSocket -v -timeout 10m` (Docker
required, host only).

### Learnings (task 8)

- The host e2e fixture uses a loose harness, an in-test Unix HTTP listener,
  and `curl --unix-socket` inside the real agent container. Interactive
  answers are queued for one `h.Run()` call, so each command still uses a
  fresh production-shaped Factory.
- The e2e Factory now mirrors the permanent process-wide `DB` noun. Commands
  continue to construct `SocketGrantStore` over that connection; the Factory
  has no table-specific noun.
- Listener-identity drift to a different uid and open-time peer-credential
  mismatch remain in the task 5 and task 6 unit suites. An unprivileged e2e
  host cannot create the required listener under a different uid, so the
  Docker journeys do not duplicate those two cases.
- Test listener paths use a short `/tmp/clawker-socket-*` directory because
  macOS rejects Unix socket names that exceed its fixed address length.
- A command-level test also uses the normal test environment, a real loose
  harness file, the start command constructor, real Unix sockets and symbolic
  links, and the real grant database. This covers canonical config expansion,
  realpath authorization, stale-grant removal, output, and bridge input in one
  production-shaped path.
- The real image runs clawkerd as root. Docker exec with `--user` omits the
  agent user's supplementary groups. The socket server therefore starts with
  root only for log setup, restores the configured agent user's full group
  set, and drops its GID and UID before socket or GPG setup. This makes a
  declared image group usable without leaving the forwarder privileged.
- The missing-group check uses a fresh image and container variant. The
  forwarder resolves the declared group before it creates the listener, so a
  failed group lookup cannot satisfy the socket-file readiness probe.
- Root harness blocks run under zsh. Its `USERNAME` parameter reports the
  active user (`root`) instead of the Docker build argument. The permission
  fixture therefore uses the stable `CLAWKER_USER` environment variable when
  it adds the agent user to its test group.
- Failed e2e tests now include the shared socket-bridge daemon log with the
  other isolated logs. A deleted listener can fail during path resolution
  before the later identity probe; both are pre-start failures.

---

## Task 9 — Docs, schemas, memories

**Goal:** ship-quality surface updates.

- CLI reference regen: `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas`
  (picks up the new `sockets` group + `--approve-grants`).
- Mintlify: new page for host socket bridges under the appropriate docs
  section (terse, end-user level, no implementation details — see
  `.claude/rules/mintlify-docs.md`); update harness-authoring docs with the
  `sockets:` manifest field, including the decision-23 contract: declare
  perms in `sockets:`, provision groups in the Dockerfile fragment.
- `clawker-plugin` support skill: if user-facing troubleshooting changed,
  follow the submodule flow (branch+PR in plugin repo, pointer bump here).
- Update `internal/db` + `internal/cmd/sockets` CLAUDE.md/AGENTS.md
  package docs (match sibling packages).
- Update `.claude/docs/ARCHITECTURE.md` / `KEY-CONCEPTS.md` if they enumerate
  subsystems.
- Update serena memory `initiative_generic_socket_bridge/PRD` status line and
  this plan's status table.

Run: `make test` clean; docs build via `npx mintlify dev` spot check.

### Learnings (task 9)

- The docs generator added the `sockets` command group, the shared
  `--approve-grants` flag, and the complete harness socket JSON schema.
- The host socket guide documents approval, non-interactive use, stored
  decisions, identity drift, revocation timing, and the Docker socket boundary.
  The harness author guide documents the full `sockets:` shape, including
  `container.group` and `container.mode`.
- The harness author and host socket guides state that the canonical reader
  expands source expressions and that prompts, grants, and bridges use the
  resolved real path.
- A harness Dockerfile must create each declared group and add `CLAWKER_USER`
  to it. `root_before_entrypoint` runs under zsh, so `USERNAME` is not the
  correct build value for this operation.
- Package docs record the permanent boundary: `DB` owns connection machinery
  only, `SocketGrantStore` owns grant verbs, and commands compose the store
  over the one Factory `DB` noun.
- No existing clawker-support known issue describes this feature or a fixed
  socket-bridge fault, so the plugin submodule needs no update.
- `make test` passed 6,628 unit tests with 11 environment-dependent skips. The
  user ran the Mintlify preview on the host and confirmed the new docs look
  correct; Mintlify is a host-side preview gate, not a container-side command.

---

## Fixed reference (verified against code 2026-08-20)

- Manifest type: `config.Manifest`, parsed in `bundler.LoadBundle`
  (`internal/bundler/bundle.go`, `HarnessManifestFile = "harness.yaml"`),
  front-door validators beside it.
- Tiers: `internal/bundle/provenance.go` — `TierFloor` (builtin, embedded),
  `TierLooseProject`, `TierLooseUser`, `TierInstalled`, `TierInPlace`;
  components carry `Provenance{Tier, Dir, Bundle, Shadows}`.
- Pre-start: `BootstrapServicesPreStart` / `BootstrapServicesPostStart` /
  `containerHarnessName` / `startSocketBridge` in
  `internal/cmd/container/shared/container_start.go`.
- Bridge manager: `internal/socketbridge/manager.go` (`EnsureBridge`,
  PID-file daemon adoption, `startBridge` spawns setsid
  `clawker bridge serve`).
- Bridge daemon cmd: `internal/cmd/bridge/bridge.go`.
- Host-side exec of container forwarder: `internal/socketbridge/bridge.go`
  (`docker exec -i <id> /usr/local/bin/clawker-socket-server`);
  `resolveHostSocket` type switch (ssh/gpg) same file.
- Container forwarder: stdlib-only
  `internal/hostproxy/internals/cmd/clawker-socket-server/main.go`; reads
  `CLAWKER_REMOTE_SOCKETS` (`consts.EnvRemoteSockets`); create-time env is
  written in `internal/docker/env.go`.
- Boot steps: `controlplane/agent/boot_steps.go` (`BootPlan`,
  `bootPlanPost` tail invariant), executor `controlplane/agent/exec.go`
  (`AgentReadyStep` releases CMD).
- Docker-socket feature (separate from bridges): `security.docker_socket`
  bind mount, validated by `validateMountableHost` in `container_create.go`;
  boot step `docker-socket` chgrps `/var/run/docker.sock`.
- XDG state: `consts.StateDir()` (`internal/consts/consts.go`).
- SQLite precedent: `controlplane/agent/registry_sqlite.go`
  (`modernc.org/sqlite`).
- Prompter: `internal/prompter` (returns raw answers; command layer maps
  them); output rules: `.claude/rules/code-style.md`.
- Root registration: `internal/cmd/root/root.go`.
