# PRD: Generic socket bridges for harnesses

Status: implementation complete; all nine plan tasks are done.
Started: 2026-08-17. Updated: 2026-08-20.

Related research: `mem:multi-harness/README` and `mem:multi-harness/follow-ups`.

## Summary

Clawker must let a harness declare a connection to a host Unix socket —
required by default, or marked optional when the harness can run without
it. Clawker must ask the user for approval at the last safe interactive
point before it starts the container. An approval applies user-wide to the
same harness principal and the same socket request until the user revokes it.

This is a generic host-access feature for third-party harness providers. The
design must not contain provider-specific bridge logic.

The feature must not bind-mount host application state. The host process
stays the single owner of its own sessions, databases, and credentials; the
container connects to that process through an approved socket bridge.

## Problem

Some harnesses need a host service to work. A harness can name that service,
but a harness declaration must not give a third-party harness automatic access
to a host socket.

The current socket bridge supports only SSH and GPG agents. Its interface and
wire data use compiled socket types. The active socket list is written into
the immutable container environment at container creation. This shape does not
support a general harness requirement or a new requirement applied to an
existing container.

Clawker has several Docker start flows because attached, detached, and restart
operations require different Docker API call orders. Authorization must not be
copied into each flow.

## Ratified product decisions

1. **One authorization point:** `BootstrapServicesPreStart` is the only point
   that checks or requests host-access approval.
2. **No create-time authorization:** `clawker create` does not prompt, grant,
   or activate host access.
3. **First-use approval:** the first `run`, `start`, or `restart` that
   needs an unapproved required bridge asks once.
4. **Persistent approval:** an accepted grant remains valid until explicit
   revocation. There is no prompt on each start.
5. **User-wide scope:** the same approved harness principal and socket request
   can be used by all projects and containers for that user on that host.
6. **Harness requirement:** a harness that declares a required bridge cannot
   run without that bridge. Denial or bridge failure prevents the harness
   command from running.
7. **Generic design:** authorization, registration, transport, lifecycle, and
   management are generic.
8. **Provenance is part of the principal:** a project-local harness with the
   same name as a built-in or installed harness cannot reuse its grant.
9. **Container certificates are not grant keys:** agent certificates are
   per-container and short-lived. They do not fit a user-wide persistent
   grant.
10. **macOS and Linux are equal targets:** the design and tests must cover
    Docker Desktop on macOS and native Docker on Linux.
11. **Clawker owns the lifecycle boundary:** only Clawker-managed `run`,
    `start`, and `restart` paths are in scope.
12. **The resolved absolute path is the approved object:** Clawker fully
    expands and symlink-resolves the host socket path before the prompt. The
    user approves that resolved path, and the grant stores it. A later start
    whose resolution differs blocks and asks again: "socket location changed
    to '<path>', trust?". The source expression is display and diagnostic
    metadata only.
13. **The harness principal is a location, not a name:** the grant key uses
    the resolved absolute path of the harness source directory, never the
    harness name or namespace, because loose harness directories can shadow
    installed names. (Built-ins never enter the grant table — decision 19.)
14. **The grant store is a new CLI-owned SQLite database:** a separate
    database file under XDG state, in a new domain store package (the only
    current SQLite consumer is the control-plane agent registry; the yaml
    `internal/storage` machinery is not used for grants). The file is never
    bind-mounted into a container, so host-only WAL access stays safe.
15. **Pre-start is the sole checkpoint:** each start reads the harness config
    as it exists now, resolves the socket path now, and checks the pair
    against the grant store. The bridge daemon receives only checked,
    approved endpoints and never reads harness manifests.
16. **Revocation applies at the next start:** `sockets revoke` deletes the
    grant. Running containers and their active bridges are not touched —
    bridge daemons are per-container and have no oversight channel, and none
    is built. The next Clawker-managed start or restart that needs the
    bridge asks again. The revoke command output states that running
    containers keep their bridges until they stop or restart.
17. **Host-to-container only; required by default, optional by marker:**
    the feature mounts host sockets into containers. A declared bridge is
    required unless it sets `optional: true` — some sockets only improve
    UX, others are load-bearing for the harness. A missing required bridge
    (unresolvable path, denial, or noninteractive without grant) fails the
    start closed; the same conditions on an optional bridge skip that
    bridge with a printed notice and the start continues. Denying an
    optional prompt skips it for that start only — no denial state is
    stored, and the next interactive start may ask again. An approved
    optional bridge behaves exactly like a required one (grant row,
    bridge, readiness barrier). There are no direction or access-mode
    fields.
18. **Approvals happen only at the request path:** the pre-start check is
    the single place a grant is created — a declared socket is requested and
    its pair is missing from the table. There is no offline grant command in
    the first version; `sockets grant` is a possible later convenience.
    Noninteractive consent is an explicit `--approve-grants` flag, shared
    from `cmdutil` by the start-capable commands. The command level decides
    the answer (prompt result or flag presence) and then calls the matching
    store function — allow or deny — which writes the row, logs the event,
    and prints the resolved path. The store functions know nothing about
    how the answer was obtained (see decision 22 for the answer set).
19. **Built-ins are inherently trusted:** harnesses shipped inside the
    Clawker binary are part of the trusted computing base — their declared
    sockets bridge without prompt or grant rows. The grant system exists
    for third-party providers: installed bundles and loose harness
    directories. Trust keys on the resolution tier, never the name: a
    loose or installed harness that shadows a built-in name is third-party
    and prompts.
20. **Autoprune per start; explicit prune for the rest:** every pre-start
    check prunes stale rows for the harness in use — rows whose socket is
    no longer declared. Other harnesses' rows are never touched at
    runtime. `sockets prune` keeps only the grants of harnesses that still
    resolve; `sockets prune -a` removes all grants. Unresolvable
    principals are removed only by the explicit prune command, never
    automatically — a missing directory can be an unmounted volume, not a
    deletion.
21. **Banned socket paths:** clawker keeps a fixed ban list of host socket
    paths that can never be declared or bridged; the Docker daemon socket
    is its first member. Every declared source is checked against the list
    — statically at manifest validation and again on every resolved path
    at pre-start. A match fails closed before any prompt, even for an
    optional bridge — no grant is possible and `--approve-grants` does not
    override, for every provenance tier including built-ins. Docker socket
    access remains exclusively the `security.docker_socket` feature.
22. **Grant rows store the answer; the prompt is four-way:** each row keeps
    a status, `allow` or `deny`. Pre-start flow: a stored row decides
    without a prompt — `allow` bridges; `deny` fails a required start
    closed (the error names the grant ID and `sockets revoke <id>` as the
    remedy) and silently skips an optional bridge (log only — this is the
    silence option). No row → interactive four-way prompt: `always` stores
    allow, `yes` allows this start only (stores nothing), `no` denies this
    start only (stores nothing), `never` stores deny. `--approve-grants`
    acts as `always` for missing rows and never overrides a stored deny.
    `sockets revoke` deletes a row of either status, returning the pair to
    the prompt path.
23. **Container-side socket identity is applied, never provisioned:** a
    declaration may set `container.group` and `container.mode` (defaults:
    the agent user's primary group and `0600`). The forwarder runs
    unprivileged as the agent user; the socket file is always owned by the
    agent user, and the declared group and mode are applied after listen.
    Creating the group and adding the agent user to it is the harness
    Dockerfile fragment's responsibility — clawker creates no users or
    groups at runtime. Canonical container users are the agent user and
    root; root needs no ownership to connect. A group or mode that cannot
    be applied fails bridge initialization and the readiness barrier stops
    the start — a harness-authoring defect to catch in testing. There is
    no `container.owner` field.
24. **Host listener identity is observed, approved, and pinned:** the
    harness declares nothing about the host listener — it cannot know
    another machine. At prompt time clawker probe-connects the resolved
    socket and reads the listener's peer credentials
    (`SO_PEERCRED`/`LOCAL_PEERCRED`); the prompt shows the observed
    identity and the user approves it. The grant stores numeric uid/gid
    (for comparison) plus owner/group names (for display). Every
    demand-driven bridge open re-verifies peer credentials against the
    granted identity; a mismatch fails closed and is logged. At pre-start,
    an observed identity that differs from the stored grant is drift: the
    changed-identity prompt asks again, exactly like a changed path.
    Built-in harness sockets have no grant row; their bridge registrations
    pin the identity observed at that start, and the open-time check
    verifies against that pin. Filesystem permissions remain the access
    gate — clawker never creates
    or modifies host users or groups; if the user cannot connect at probe
    time the start fails closed.

## User stories

- As a user, I can select a harness that needs a host service and see the exact
  host access it requests before the container starts.
- As a user, I approve that exact request once and do not receive the same
  prompt on later starts or restarts.
- As a user, I can list and revoke saved approvals.
- As a user in a noninteractive environment, I receive a closed failure that
  tells me to re-run with `--approve-grants` or run an interactive start
  once to approve the declared access.
- As a harness author, I can declare a required socket without adding Go code
  for a new socket type.
- As a harness author, I can mark a socket optional so my harness still
  runs when the user declines it or the host service is absent.
- As a security reviewer, I can identify the harness source, socket source,
  container destination, and approval time for each grant.

## Required behavior

### Harness declaration

A harness manifest can declare one or more required socket bridges. Field
names are ratified:

```yaml
# harness.yaml
sockets:
  - source: $AGENTD_HOME/control.sock
    target: /home/clawker/.agentd/control.sock
    purpose: Connects the container agent to the host agentd daemon.
    optional: true
    container:
      group: agentd
      mode: "0660"
```

`source` is the host socket expression, `target` the absolute container
destination, and `purpose` the user-facing reason — all three required.
A fourth field, `optional: true`, marks a bridge the harness can run
without (default false — see decision 17). An optional `container` block
sets the socket file's group and mode inside the container
(`container.group`, `container.mode`; defaults: the agent user's primary
group and `0600`) — see decision 23 for the provisioning contract. The
harness declares nothing about the host listener (decision 24).

The only direction is a container client connecting to a host socket.
There are no direction or access-mode fields; such fields are added only
when a real consumer needs them. The schema is generic and must not grow provider-specific fields such
as `ssh` or `gpg`. Banned socket paths cannot be declared (decision 21); the Docker daemon
socket is banned — its access is available only through the
`security.docker_socket` setting.

The resolved absolute host path is the approved object. Source-expression
syntax is standard: `$VAR` / `${VAR}` environment expansion plus a leading
`~` for the user home — no command substitution, no globs. Clawker expands
the source expression and resolves symlinks before the prompt; the prompt shows
only the fully resolved path — never variables or symlinks — so the user
knows truly what they approve. Expansion inputs
come only from the host process environment, never from project files or
project config. A path whose resolution changes between host sessions
re-prompts on each change; this is accepted for application sockets with
stable homes.

### Pre-start authorization

`BootstrapServicesPreStart` must:

1. Inspect the target container.
2. Confirm that it is Clawker-managed.
3. require an explicit harness identity for privileged host access;
   the existing fallback to the current default harness must never grant host
   access.
4. Resolve the harness principal and its provenance.
5. Resolve its required socket declarations: expand each source expression
   from the host environment and resolve symlinks to an absolute path.
   Probe-connect each resolved socket and read the observed listener
   identity (decision 24).
6. Check each (principal, resolved request) pair against the user-wide grant
   store. A stored row's status decides without a prompt (decision 22). A
   changed resolved path is a missing grant; a changed observed listener
   identity is drift and re-prompts likewise (decision 24).
7. Ask through an injected command-layer authorizer when a grant is missing.
   A drifted path uses the changed-location prompt; acceptance removes the
   old row and stores the new pair.
8. Stop before Docker start if a required bridge is denied or cannot be
   authorized; skip an optional bridge in the same conditions with a
   printed notice and continue.
9. Record an accepted grant through the same domain service used by the
   management command.

The pre-start function coordinates the check but does not implement terminal
input. A command-factory noun owns the permission store, `IOStreams`, and the
prompter. Attached start paths already run pre-start in cooked terminal mode
before Docker attach takes ownership of the terminal.

Normal restart calls pre-start before `ContainerRestart`. Signal-based
restart calls the shared `ContainerStart`, which also calls pre-start. No
restart-specific permission logic is permitted.

### Prompt

The prompt identifies:

- the harness and its principal location;
- the fully resolved host socket path — never a variable or symlink;
- the container destination;
- the observed host listener identity — owner, group, uid, gid (decision
  24);
- the declared purpose under a `Purpose:` label — harness-authored,
  untrusted text, never mixed with Clawker-verified facts;
- that the bridged host service is an egress path outside the firewall.

The question is a single-line letter prompt with four answers — one-time
and persistent on both sides, no bias and no default; an empty response
asks again. Input accepts the single letter or the full word,
case-insensitive.

Example:

> Installed harness `acme/agentd` requires access to a host socket.
>   Socket:  /Users/andrew/.agentd/control.sock
>   Mounted: /home/clawker/.agentd/control.sock
>   Purpose: connects the container agent to the host agentd daemon.
>   Listener: root:agentd (uid 0, gid 989)
> Traffic on this bridge bypasses the egress firewall.
> Allow this socket bridge? [a]lways / [y]es / [n]o / ne[v]er:

Answer semantics (decision 22): `always` stores an allow grant; `yes`
bridges this start only and stores nothing; `no` denies this start only
and stores nothing; `never` stores a deny grant. The prompt for an
optional bridge additionally states that the harness runs without it if
declined. Denial of a required bridge prevents Docker start; denial of an
optional bridge skips it for that start only. A noninteractive call never
assumes consent; the explicit `--approve-grants` flag is the only
noninteractive consent — it acts as `always` for missing rows, prints each
approved resolved path, and never overrides a stored deny.

### Persistent grant

Grants are rows in a new CLI-owned SQLite database: a separate database file
under XDG state, in its own domain store package (same driver as the
control-plane agent registry, `modernc.org/sqlite`, which is today's only
SQLite consumer). Only host CLI processes touch the file and it is never
bind-mounted into a container, so WAL is safe here — unlike the CP registry,
which crosses a container bind boundary. Concurrent-command safety comes
from the database, not file locking. Do not use the yaml `internal/storage`
machinery: grants are machine state, not user config. Do not extend the
current CLI update-state store: that store has a narrow update-data contract
and a single-writer assumption.

A grant is the pair (harness, socket):

- the harness principal: the resolved absolute path of the harness source
  directory;
- the resolved absolute host socket path.

The container destination is not part of the grant: the user approves host
exposure; where the harness mounts the socket inside its own container is
the harness's business.

Schema:

```sql
CREATE TABLE grants (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,  -- the grant ID: shown by list, passed to revoke
    harness_path TEXT NOT NULL,  -- realpath of harness source dir
    harness_name TEXT NOT NULL,  -- human name, display only
    host_path    TEXT NOT NULL,  -- resolved absolute host socket path
    status       TEXT NOT NULL CHECK (status IN ('allow','deny')),  -- stored answer (decision 22)
    host_uid     INTEGER NOT NULL,  -- observed listener uid at approval (decision 24)
    host_gid     INTEGER NOT NULL,  -- observed listener gid at approval
    host_owner   TEXT NOT NULL,     -- owner name at approval, display only
    host_group   TEXT NOT NULL,     -- group name at approval, display only
    purpose      TEXT NOT NULL,  -- declaration's purpose text, display only
    granted_at   TEXT NOT NULL,  -- RFC3339
    UNIQUE (harness_path, host_path)
);
```

Drift acceptance deletes the old row and inserts the new pair; rows whose
socket is no longer declared by their harness are pruned by the pre-start
check (decision 20). Schema version uses `PRAGMA
user_version`. The grant ID shown by `sockets list` is the auto-increment
integer primary key, assigned by the database; it exists only as a user
handle to pass to `sockets revoke`.

The key does not contain a project, container ID, certificate thumbprint, or
bundle version. The principal is the install directory as it is: if an
install layout puts the version in the path, a version change moves the
principal and the next start re-prompts — accepted behavior, not special
cased. A changed request gets a new identity and requires approval.

The approval time is stored for `sockets list`; grant, denial, and revoke
details live in the logs.

### Provenance and identity

The harness source location provides the persistent permission principal:
the realpath-normalized absolute path of the harness source directory. A
loose project-local harness that shadows an installed name therefore gets
its own principal automatically — the permission check can never resolve a
different same-named harness and treat it as the original. Built-in
harnesses never appear in the grant table: they are inherently trusted
(decision 19), and trust is decided by resolution tier, not name. Whoever
can write inside a granted harness directory
inherits its grant; the damage is bounded because the grant covers only the
already-approved resolved socket path, and any socket change re-prompts.

Container identity limits where Clawker activates an approved bridge. Clawker
must target the inspected container ID. Agent mTLS continues to protect CP
communication after startup, but its certificate and registry thumbprint do
not determine persistent host-access approval. A first-start container does
not yet have a CP registry row, and certificate rotation must not remove a
user-wide grant.

### Post-start activation and readiness

Docker must start before the current bridge server can use `docker exec`.
After Docker start, Clawker registers only the approved socket requests for the
target container.

Active bridges — required ones plus approved optional ones — are part of
the start transaction (a skipped optional bridge is not):

- the harness command must not run before every active bridge is ready;
- a bridge initialization failure must not release the command;
- failure cleanup must leave no orphan bridge process or active registration;
- restart must re-establish missing bridge processes and remain idempotent.

The current CP can dispatch `AgentReady` while post-start setup is still in
progress. The implementation needs a generic readiness barrier before the user
command. The barrier design is not ratified. Candidate locations are the
clawkerd command-release path or a CP readiness prerequisite. CP failures must
degrade without panic or process exit, as required by the CP security rules.

### Generic bridge registration

Replace the compiled SSH/GPG type switch with opaque authorized registration
identifiers. The container side can open only registrations supplied for its
container. The host maps each registration to its already approved host
endpoint.

The active socket registrations must be supplied at start time. Do not depend
on `CLAWKER_REMOTE_SOCKETS` from the container's immutable create-time
environment. Stable application socket paths can remain fixed. If an
application needs a dynamic environment value, clawkerd must add it when it
starts the user command.

Opening the approved bridge does not connect to the host endpoint
unconditionally. The host connection remains demand-driven when the container
client opens the registered socket. At each demand-driven open, the bridge
connects only to the stored resolved path and then verifies the listener's
peer credentials (`SO_PEERCRED` / `LOCAL_PEERCRED`) against the granted
identity (decision 24): the observed uid/gid must equal the approved
uid/gid. This closes the resolve-to-connect race that a path comparison
alone cannot close.

The bridge daemon is a separate detached process; registrations reach it
through a file or stdin handoff, not argv. There is no crash supervision:
a bridge that dies mid-session cannot be reliably detected or surfaced,
and no mechanism pretends to. The daemon and forwarder log lifecycle and
failure events; those logs are the forensic record for root-cause and
post-mortem work. Dead bridged sockets stay dead until the next
Clawker-managed start or restart re-establishes the bridge. A container
started outside Clawker paths (for example by a Docker restart policy)
never re-registers sockets — the readiness barrier holds the user command,
and the logs are the diagnostic record. `EnsureBridge` changes to an
options-struct signature that carries the container ID, the internal SSH/GPG
switches, and the checked registrations.

Existing SSH and GPG forwarding must keep working. They can migrate onto the
generic registration engine, but their current optional project settings are
not changed by this PRD.

### Management commands

A visible host-access management group is required. The group name is
ratified: `sockets` — narrow on purpose, so it cannot collide with future
host-access features. The working command shape is:

- `clawker sockets list`
- `clawker sockets info <grant-id>`
- `clawker sockets revoke <grant-id>`
- `clawker sockets revoke --harness <name>`
- `clawker sockets revoke --all`
- `clawker sockets prune` (keep only grants of harnesses that resolve)
- `clawker sockets prune -a` (remove all grants)

There is no offline grant command in the first version: every approval
happens at the pre-start check, when a declared socket is requested and its
pair is missing from the grant table. A later `sockets grant --harness`
convenience command is a possible follow-up; if added, it must load the
harness declarations (never accept raw socket paths as flags), show the same
disclosure as the interactive prompt, and write through the same authorizer
and store.

`list` supports normal table output and the project-standard machine formats.
`list` shows only the grant ID, harness name, status (allow/deny), and
purpose — purpose is stored so users reviewing grants over time remember
what each was for. `info <grant-id>` is the full formatted dump of one
grant: id, harness name, principal path, host socket path, status,
observed listener identity, purpose, and decision time. Both use the
standard color scheme and table/detail presentation.

## Security requirements

- A harness declaration alone never grants host access.
- Missing or ambiguous harness identity fails closed for a required bridge.
- The configured default harness is not an authorization fallback.
- A same-named harness from different provenance does not reuse a grant.
- A changed resolved host path requires a new grant.
- Noninteractive startup without a grant fails closed.
- The bridge can connect only to the endpoint in its approved registration.
- A required bridge failure prevents the harness command from running.
- Permission state lives in the CLI-owned SQLite grant store; concurrent
  Clawker commands must not lose updates.
- Logs record grant, denial, revoke, activation, and activation failure events
  without logging socket traffic or secrets. Each grant event records the
  harness principal, the resolved host path, and the time — the log is the
  forensic record; the grant table holds only the current approvals. The bridge also logs connection
  open and close events (metadata only) — a bridged host service is an egress
  path outside the firewall, and connection events are its audit floor.
- Path expansion reads only the host process environment, never project files
  or project config.
- A declared socket source that is or resolves to a banned socket path (a
  fixed ban list; the Docker daemon socket is its first member) fails
  closed before any prompt or grant, even when marked optional;
  `--approve-grants` does not override, for every provenance tier
  (decision 21).
- After connect, the bridge verifies listener peer credentials; a listener
  not matching the granted identity fails closed.
- The prompt separates Clawker-verified facts from harness-authored purpose
  text.
- SSH and GPG forwarding remain governed by project settings; grants never
  gate them and they never gate grants.
- No new behavior is gated on the firewall setting. The CP and identity
  systems remain unconditional infrastructure.
- No CP boot or serve path can panic, call `log.Fatal`, or call `os.Exit`
  because of this subsystem.

## Product boundaries

### In scope

- Generic required Unix socket declarations for harnesses.
- User approval and persistent user-wide grants.
- Provenance-aware permission principals.
- Grant management commands.
- Start and restart authorization.
- Generic start-time socket registration.
- A required-service readiness barrier.
- macOS and Linux support.

### Out of scope

- Host directory permission management.
- Direct bind mounts of host application state (databases, sessions, locks).
- Arbitrary host command execution declared by a harness.
- A general host-service process manager in the first socket-bridge feature.
- Direct Docker CLI lifecycle paths outside Clawker.
- Certificate thumbprints as persistent permission keys.
- Per-project or per-container grants.
- Network socket bridges unless a later PRD adds them.

## Acceptance criteria

1. `clawker create` for a harness with a required bridge does not prompt and
   does not activate the bridge.
2. First interactive `run` or `start` shows one provenance-aware prompt
   before Docker start.
3. `always` writes a user-wide allow grant and continues startup; `yes`
   continues startup without writing anything.
4. A later start or restart with the same principal and request does not
   prompt.
5. Denial of a required bridge prevents Docker start; denial of an optional
   bridge skips it and the start continues.
6. Noninteractive first use of a required bridge fails closed and prints
   both remedies:
   `--approve-grants` and an interactive start. With `--approve-grants`,
   the same grants are written as through the prompt, and each approved
   resolved path is printed.
7. Revocation causes the next required start to ask again.
8. A changed socket request asks again, and a changed resolved host path
   shows the changed-location prompt.
9. A harness that shadows a built-in name resolves at its own tier,
   receives no built-in trust, and prompts.
10. A missing harness label cannot use the configured default to obtain host
    access.
11. Both restart flows check permission and re-establish the bridge.
12. The user command cannot run before all active bridges report ready;
    skipped optional bridges are not waited on.
13. Post-start bridge failure does not run the user command and cleans up its
    partial state.
14. Concurrent grant or revoke commands do not lose state.
15. Existing SSH and GPG forwarding tests continue to pass.
16. Unit tests cover normalization, grant identity, provenance separation,
    store locking, authorization decisions, prompt behavior, and error
    messages.
17. Integration tests cover first run, later start, restart, revoke, changed
    request, noninteractive use, and readiness failure.
18. The bridge works on native Linux Docker and Docker Desktop on macOS.
19. A socket bridge never mounts any host state directory into the container.
20. A listener identity mismatch against the granted identity fails closed
    at bridge open and is logged; the same mismatch at the pre-start probe
    re-prompts as drift.
21. Project configuration cannot influence host path expansion.
22. A symlink or path swap after approval cannot silently retarget the
    bridge: drift re-prompts at start, and peer-credential verification
    rejects a foreign listener at open.
23. A harness that declares a banned socket path (first member: the Docker
    daemon socket) fails closed before any prompt or grant, even if the
    bridge is marked optional; `--approve-grants` does not change the
    outcome.
24. An optional bridge that is unresolvable, denied with `no`, or
    unapproved in a noninteractive start is skipped with a printed notice
    and the start continues; nothing is persisted. An approved optional
    bridge behaves exactly like a required one.
25. A stored `deny` suppresses the prompt: a required start fails closed
    naming the grant ID and `sockets revoke <id>`; an optional bridge is
    skipped silently (log only). `--approve-grants` does not override a
    stored deny.
26. The container-side socket file is owned by the agent user with the
    declared group and mode applied; a group or mode that cannot be
    applied fails bridge initialization and the user command does not run.
27. The prompt shows the observed listener identity, and the grant stores
    it; a start whose observed identity differs from the grant re-prompts.

## Delivery sequence

1. Add the manifest contract, normalized request model, permission domain,
   the new SQLite grant store package, mocks, and management commands using
   TDD.
2. Inject the authorizer into the shared pre-start path and add
   provenance-aware checks without changing create-time authorization.
3. Generalize the socket bridge protocol and supply registrations at start
   time; preserve SSH and GPG behavior.
4. Add the required-service readiness barrier and failure cleanup.
5. Test on Linux and macOS.
6. Update architecture, design, package references, CLI docs, Mintlify docs,
   schemas, README content, and Serena memories.

## Open design decisions

None — the product definition is complete.

## Current code facts

- `BootstrapServicesPreStart` is shared by Clawker-managed start flows and has
  access to real command `IOStreams`.
- Normal restart calls pre-start directly before `ContainerRestart`.
- Signal-based restart uses the shared `ContainerStart`.
- `containerHarnessName` currently falls back to the configured default when
  the container has no harness label. That fallback must not authorize host
  access.
- `BootstrapServicesPostStart` currently starts the SSH/GPG socket bridge.
- `SocketBridgeManager.EnsureBridge(containerID, gpgEnabled)` is
  provider-specific.
- The current container socket server reads `CLAWKER_REMOTE_SOCKETS`, which
  is fixed in the Docker environment at container creation.
- The current bridge host process uses `docker exec -i` and opens host sockets
  on demand. It is a setsid-detached daemon (`clawker bridge serve`) tracked
  by a PID file; it survives CLI exit and is re-adopted by `EnsureBridge`.
- Bundle components already carry resolution provenance with tier, directory,
  bundle identity, and shadow information.
- Agent certificates bind agent and container identity for CP communication,
  but the CP registry row is written only after the agent registers.
