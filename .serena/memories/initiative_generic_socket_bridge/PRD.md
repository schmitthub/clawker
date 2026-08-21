# PRD: Generic required socket bridges for harnesses

Status: product definition; implementation is not started.
Started: 2026-08-17.
First use case: Codex App Server access from the Codex harness.

Related research: `mem:agent-sandbox-research/providers/codex-cli-sandbox`,
`mem:multi-harness/README`, and `mem:multi-harness/follow-ups`.

## Summary

Clawker must let a harness declare a required connection to a host Unix
socket. Clawker must ask the user for approval at the last safe interactive
point before it starts the container. An approval applies user-wide to the
same harness principal and the same socket request until the user revokes it.

This is a generic host-access feature. Codex App Server is the first consumer,
but the design must not contain Codex-specific bridge logic.

The feature must not bind-mount Codex state directories or databases. The
preferred Codex design keeps the host Codex process as the owner of sessions,
memory, authentication, and SQLite state. The container connects to that
process through an approved socket bridge.

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
   management are generic. Codex is a consumer.
8. **Provenance is part of the principal:** a project-local harness with the
   same name as a built-in or installed harness cannot reuse its grant.
9. **Container certificates are not grant keys:** agent certificates are
   per-container and short-lived. They do not fit a user-wide persistent
   grant.
10. **macOS and Linux are equal targets:** the design and tests must cover
    Docker Desktop on macOS and native Docker on Linux.
11. **Clawker owns the lifecycle boundary:** only Clawker-managed `run`,
    `start`, and `restart` paths are in scope.

## User stories

- As a user, I can select a harness that needs a host service and see the exact
  host access it requests before the container starts.
- As a user, I approve that exact request once and do not receive the same
  prompt on later starts or restarts.
- As a user, I can list and revoke saved approvals.
- As a user in a noninteractive environment, I receive a closed failure and a
  command that can grant the declared access before startup.
- As a harness author, I can declare a required socket without adding Go code
  for a new socket type.
- As a security reviewer, I can identify the harness source, socket source,
  container destination, and approval time for each grant.

## Required behavior

### Harness declaration

A harness manifest can declare one or more required socket bridges. Each
declaration has:

- a stable bridge identifier;
- a host socket source expression;
- an absolute container socket destination;
- the connection direction and access mode;
- a required marker;
- a user-facing purpose.

The final schema name and field names are not ratified. The schema must be
generic and must not use provider-specific fields such as `codex`, `ssh`, or
`gpg`.

The host source expression is the approved object. Clawker also shows the
expanded path in the prompt. This distinction is necessary for stable
expressions such as an environment-based agent socket whose resolved path can
change between host sessions.

### Pre-start authorization

`BootstrapServicesPreStart` must:

1. Inspect the target container.
2. Confirm that it is Clawker-managed.
3. require an explicit harness identity for privileged host access;
   the existing fallback to the current default harness must never grant host
   access.
4. Resolve the harness principal and its provenance.
5. Resolve and normalize its required socket declarations.
6. Check each request against the user-wide permission store.
7. Ask through an injected command-layer authorizer when a grant is missing.
8. Stop before Docker start if access is denied or cannot be authorized.
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

- the harness and its provenance;
- the exact host source expression and current expanded path;
- the container destination;
- the declared purpose;
- that the approval remains until revocation.

Example:

> Built-in harness `codex` requires access to the host Codex App Server
> socket to run. Allow `$CODEX_HOME/app-server-control/app-server-control.sock`
> on the host at `/home/clawker/.codex/app-server.sock` in Clawker containers?
> This approval remains until you revoke it.

A denial prevents Docker start. A noninteractive call never assumes consent.

### Persistent grant

A grant is stored in a separate, locked, XDG state file. Do not extend the
current CLI update-state store: that store has a narrow update-data contract
and a single-writer assumption.

The stable grant identity is derived from:

- harness identity;
- harness provenance identity;
- bridge identifier;
- normalized host source expression;
- normalized container destination;
- connection direction and access mode.

The key does not contain a project, container ID, certificate thumbprint, or
bundle version. A harmless bundle version update therefore keeps the grant
when its principal and requested access are unchanged. A changed request gets
a new identity and requires approval.

Stored display and audit data includes the approval time and the provenance
details that were shown to the user. The store must support concurrent Clawker
commands without lost updates.

### Provenance and identity

Bundle provenance provides the persistent permission principal. At minimum, it
must distinguish built-in, installed-bundle, and project-local harnesses and
must retain the bundle identity or source scope.

The resolved harness provenance must be bound to the harness image or another
runtime identity that a later start can inspect. The exact implementation is
not ratified. The permission check must not silently resolve a different
same-named harness at restart and treat it as the original principal.

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

Required bridges are part of the start transaction:

- the harness command must not run before every required bridge is ready;
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
client opens the registered socket.

Existing SSH and GPG forwarding must keep working. They can migrate onto the
generic registration engine, but their current optional project settings are
not changed by this PRD.

### Management commands

A visible host-access management group is required. The working command shape
is:

- `clawker access list`
- `clawker access grant --harness <name>`
- `clawker access revoke <grant-id>`
- `clawker access revoke --harness <name>`
- `clawker access revoke --all`

The command-group name and exact flags are proposed, not ratified.

`grant --harness` loads that installed harness and grants only its current
declared requests. It must not accept arbitrary raw socket paths as command
flags. The interactive prompt and management command write through the same
authorizer and store.

`list` supports normal table output and the project-standard machine formats.
It shows the grant ID, harness, provenance, bridge identifier, host source,
container destination, and approval time.

## Security requirements

- A harness declaration alone never grants host access.
- Missing or ambiguous harness identity fails closed for a required bridge.
- The configured default harness is not an authorization fallback.
- A same-named harness from different provenance does not reuse a grant.
- A changed host source, destination, direction, or access mode requires a new
  grant.
- Noninteractive startup without a grant fails closed.
- The bridge can connect only to the endpoint in its approved registration.
- A required bridge failure prevents the harness command from running.
- Permission state uses atomic writes and a cross-process lock.
- Logs record grant, denial, revoke, activation, and activation failure events
  without logging socket traffic or secrets.
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
- Migration of the Codex harness to the generic feature after the bridge is
  proven.

### Out of scope

- Host directory permission management.
- Direct bind mounts of Codex SQLite, session, memory, lock, or snapshot state.
- Automatic approval for built-in harnesses.
- Arbitrary host command execution declared by a harness.
- A general host-service process manager in the first socket-bridge feature.
- Direct Docker CLI lifecycle paths outside Clawker.
- Certificate thumbprints as persistent permission keys.
- Per-project or per-container grants.
- Network socket bridges unless a later PRD adds them.

## Codex first use case

Official Codex documentation describes App Server as the process that owns
authentication, conversation state, approvals, and event streams. The current
Codex CLI also has experimental remote TUI and execution-server paths.

Target shape:

```
host Codex App Server
        |
 approved Unix socket bridge
        |
container Codex client / execution environment
```

The host remains the single writer for Codex sessions, memory, and SQLite
state. This avoids cross-platform SQLite WAL sharing and host-state corruption.

The generic bridge feature does not by itself approve or implement host process
startup. Separate Codex integration work must determine:

- whether Clawker starts the official App Server daemon or requires it to be
  running;
- whether the experimental remote and execution-server interfaces are ready
  for supported use;
- how all command, patch, hook, MCP, skill, HTTP, and shell-command execution
  stays inside the container;
- how the container execution environment becomes the selected environment
  without unsafe host configuration changes.

Official references:

- https://learn.chatgpt.com/docs/app-server
- https://learn.chatgpt.com/docs/config-file/config-advanced
- https://learn.chatgpt.com/docs/customization/memories

## Acceptance criteria

1. `clawker create` for a harness with a required bridge does not prompt and
   does not activate the bridge.
2. First interactive `run` or `start` shows one provenance-aware prompt
   before Docker start.
3. Acceptance writes a user-wide grant and continues startup.
4. A later start or restart with the same principal and request does not
   prompt.
5. Denial prevents Docker start.
6. Noninteractive first use fails and prints an exact grant command.
7. Revocation causes the next required start to ask again.
8. A changed socket request asks again.
9. A project-local harness cannot reuse a built-in same-name grant.
10. A missing harness label cannot use the configured default to obtain host
    access.
11. Both restart flows check permission and re-establish the bridge.
12. The user command cannot run before all required bridges report ready.
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
19. The Codex proof does not mount any candidate Codex host-state directory.

## Delivery sequence

1. Add the manifest contract, normalized request model, permission domain,
   persistent store, mocks, and management commands using TDD.
2. Inject the authorizer into the shared pre-start path and add
   provenance-aware checks without changing create-time authorization.
3. Generalize the socket bridge protocol and supply registrations at start
   time; preserve SSH and GPG behavior.
4. Add the required-service readiness barrier and failure cleanup.
5. Test on Linux and macOS.
6. Build a Codex App Server proof with the generic bridge.
7. Update architecture, design, package references, CLI docs, Mintlify docs,
   schemas, README content, and Serena memories.

## Open design decisions

These decisions must be resolved before implementation:

1. Exact harness manifest field names and validation rules.
2. Exact public command-group name and flags.
3. Stable representation of harness provenance in the image/runtime identity.
4. Readiness-barrier ownership between the CLI, CP, and clawkerd.
5. Start-time delivery mechanism for dynamic application environment values.
6. Host source-expression syntax and allowed expansion inputs.
7. Codex App Server lifecycle policy.
8. Maturity policy for Codex experimental remote interfaces.

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
  on demand.
- Bundle components already carry resolution provenance with tier, directory,
  bundle identity, and shadow information.
- Agent certificates bind agent and container identity for CP communication,
  but the CP registry row is written only after the agent registers.
