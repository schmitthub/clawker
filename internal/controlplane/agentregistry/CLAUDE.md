# agentregistry Package

Persisted record of (cert thumbprint, container_id, project, agent_name, canonical_cn) for every clawker-managed container. Rows are written by the CLI at container create time alongside auth material delivery. Consumed by `agent.IdentityInterceptor` (every non-opted-out AgentPort RPC — opt-out roster is empty in this branch since AgentService has no inbound RPCs), `AdminService.ListAgents`, and the local-read `clawker controlplane agents` CLI. Eviction is owned by the CP: startup `Reap` against live docker state, plus dockerevents `ContainerRemoved` (destroy) in steady state.

## Backing store: sqlite (cache-less)

The registry persists to a sqlite database under the CP-owned data subdirectory:

- Host: `<DataDir>/controlplane/controlplane.db`
- Container: `consts.CPControlPlaneDBPath` (bind-mounted RW into `consts.CPControlPlaneDir`)
- Driver: `modernc.org/sqlite` (pure Go — no cgo toolchain in the CP build pipeline)

There is **no in-memory cache**. Every `Lookup` / `Snapshot` / `LookupByThumbprint` / `LookupByContainerID` issues a fresh sqlite query. This was a deliberate decision in the Task #1 PR fix: the previous mirror-with-disk-as-source-of-truth design accumulated a class of drift bugs (orphan rows when DELETE failed but memory eviction proceeded; malformed rows resurrected on reload; partial-evict invariant violations) that all collapse to zero by construction once the cache is gone. modernc/sqlite + WAL on local fs makes per-RPC reads sub-millisecond, and the AgentPort RPC count is bounded by the per-host agent count (single digits). The DB survives CP container recreation.

## Schema

```sql
CREATE TABLE IF NOT EXISTS agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  canonical_cn   TEXT NOT NULL,
  registered_at  INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX IF NOT EXISTS idx_name_project ON agents(project, agent_name);
```

`thumbprint_hex` and `container_id` are individually UNIQUE: a given cert can bind to exactly one container and a given container can host exactly one registration. The composite PK encodes the binding intent. `(project, agent_name)` is non-unique — Docker container name uniqueness handles the real conflict upstream; brief stale rows during eviction races are tolerated.

`canonical_cn` is the pre-computed `clawker[.<project>].<agent>` string composed by `auth.CanonicalAgentCN` from typed inputs at `Add` time. Storing it as a column eliminates the historical Lookup-time panic vector where `auth.MustProjectSlug` / `auth.MustAgentName` reconstructed the CN from on-disk strings on every read — a single malformed row used to crash the entire CP. `Lookup` now compares the supplied peer-cert CN against this column with `subtle.ConstantTimeCompare`; no reconstruction, no Must*.

`last_seen` mirrors `registered_at` today; future per-agent RPCs will refresh it at their own boundary.

### Schema apply is transactional

The `CREATE TABLE` + `CREATE INDEX` block runs inside an explicit `BEGIN/COMMIT` so an interrupted apply (process crash mid-DDL) leaves the DB in a coherent state. A pre-Task-01 `agents` table that lacks the `canonical_cn` column is detected via `PRAGMA table_info(agents)` and dropped+recreated on the next writer open. This is alpha-policy: the registry is regenerable on the next clawker run/start, and a forced re-create of every affected container beats a partial migration. The drop is logged at Warn so operators see it.

## Identity contract

Identity in agentregistry is `(thumbprint, container_id)` — both UNIQUE in sqlite. `agent_name` and `project` are CROSS-CHECK fields stored alongside, NOT identity. The canonical CN derived from `(project, agent_name)` is what `Lookup` compares against the peer cert; storing it pre-computed is what defangs the Must* panic vector.

Add takes string `Project` + `AgentName` on `Entry` and validates them via `auth.NewProjectSlug` / `auth.NewAgentName` (the err-returning typed constructors). Malformed inputs surface as a returned error from `Add`, NOT as a panic. The remaining programming-error invariants (zero thumbprint, empty `ContainerID`, zero `RegisteredAt`) still panic at the call site because they cannot originate from user input — they would mean a wiring bug in the in-package `agent.Handler`.

## Registry row ↔ extant container invariant

A row exists ⇔ the named container exists in docker (running OR stopped). Eviction triggers narrowed to:

- **`docker rm` (`dockerevents.ContainerRemoved`)** — steady-state, driven by the dockerevents subscription. Container is gone for good, row is orphaned.
- **CP startup reap** — `agentregistry.Reap(ctx, reg, lister, log)` lists every `purpose=agent` container with `All: true` (running + stopped + exited) and evicts every row whose container_id is missing. Heals the registry against `docker rm` events that landed while the CP was down.

Stop/die/kill do NOT evict — a stopped container can be `docker start`-ed back into life and the row should pick up where it left off.

## Writers

The CLI is the row creator (one `Add` per `clawker run`/`start`). The CP is the row evictor (Reap on startup, dockerevents on destroy). Both open the sqlite DB via `NewSQLiteWriter`; sqlite serializes the two writers via its file lock and `busy_timeout(5000)` absorbs transient contention. `NewSQLiteReader` (mode=ro+query_only) remains for read-only consumers like the `clawker controlplane agents` CLI.

## API

```go
type Entry struct {
    AgentName    string
    Project      string
    ContainerID  string
    Thumbprint   [sha256.Size]byte
    RegisteredAt time.Time
    LastSeen     time.Time
}

type Registry interface {
    Add(entry Entry) error                                            // Validates + persists; returns err on malformed identity OR sqlite write failure.
    Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error)   // CN-gated identity resolution (IdentityInterceptor).
    LookupByThumbprint(thumbprint [sha256.Size]byte) (*Entry, error)  // No CN check; symmetric peer of LookupByContainerID, kept for any future direct-thumbprint lookup.
    LookupByContainerID(containerID string) (*Entry, error)           // Used by agentdial post-handshake provenance lookup.
    EvictByContainerID(containerID string) error                      // Returns underlying DELETE error so callers log-and-proceed.
    Snapshot() []Entry
}

func NewRegistry(log *logger.Logger) Registry              // In-memory only — used by tests.
func NewSQLiteWriter(dbPath string, log *logger.Logger) (Registry, error)
func NewSQLiteReader(dbPath string, log *logger.Logger) (Registry, error)
func EnsureSchema(dbPath string, log *logger.Logger) error // Idempotent: opens writer, applies schema, closes.
```

`Add` returns an error when:
- `auth.NewProjectSlug` rejects `Project` (malformed slug).
- `auth.NewAgentName` rejects `AgentName` (empty or invalid characters).
- The sqlite INSERT fails (UNIQUE collision against a stale row, disk full, schema corruption).

`Add` panics on programming-error invariants the call site MUST have checked: zero thumbprint, empty `ContainerID`, zero `RegisteredAt`.

`EvictByContainerID` returns the underlying DELETE error so the caller can log it. The dockerevents-driven and reaper-driven callers log-and-proceed (they cannot retry from a delta consumer; the next reap pass heals); the CLI-side `clawker container remove` logs at debug since registry hiccups must not surface as remove failures.

## Subscriber

`Subscribe(ctx, registry, bus, log)` subscribes the registry to typed `dockerevents.ContainerRemoved` events on the Overseer bus; **destroy only** drives `EvictByContainerID`. Stop/die/kill are deliberately ignored — see "Registry row ↔ extant container invariant" above. The handler logs Evict errors at Warn and proceeds — it cannot retry from a bus consumer because the next event is already queued. Reused by both in-memory and sqlite-backed registries; the subscription only sees the Registry interface.

## Reaper

`Reap(ctx, registry, lister, log)` runs once at CP startup. The lister enumerates every `purpose=agent` container with `All: true` (running + stopped + exited) so a stopped container with a live row survives the sweep. The lister is retried with bounded exponential backoff (3 attempts: 100ms → 200ms → 400ms) before giving up — a transient docker-daemon hiccup at CP boot must not skip the first sweep entirely (the dockerevents subscription only catches NEW destroys from that point forward). Per-row eviction errors are aggregated into the returned error via `errors.Join` so the caller can surface them; the count reflects only successful evictions.

## Mock

moq-generated `RegistryMock` under `mocks/`. Regenerate via `cd internal/controlplane/agentregistry && go generate ./...`.

## Imports

**Uses**: `internal/auth` (`CanonicalAgentCN`, `NewAgentName`, `NewProjectSlug`), `internal/logger`, `modernc.org/sqlite`, stdlib `crypto/sha256`, `crypto/subtle`, `database/sql`, `encoding/hex`.

**Used by**: `cmd/clawker-cp/main.go` (NewSQLiteWriter construction + Reap + Subscribe wiring + Dialer construction), `internal/controlplane/server.go` (ListAgents), `internal/controlplane/agent` (IdentityInterceptor), `internal/controlplane/agentdial` (post-handshake Provenance lookup), `internal/cmd/container/shared/agent_bootstrap.go` (CLI write path), `internal/cmd/container/remove/remove.go` (CLI evict path), `internal/cmd/controlplane/agents.go` (NewSQLiteReader for local-read CLI), `internal/controlplane/cpboot/bootstrap.go` (EnsureSchema before CP container start).
