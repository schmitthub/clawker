# agentregistry Package

Persisted record of (cert thumbprint, container_id, project, agent_name) for every clawker-managed container. Rows are written by the CLI at container create time alongside auth material delivery. Consumed by `agent.IdentityInterceptor` (every non-Connect AgentPort RPC), `AdminService.ListAgents`, and the local-read `clawker controlplane agents` CLI. Eviction is owned by the CP: startup `Reap` against live docker state, plus dockerevents `DeltaRemoved` (destroy) in steady state.

## Backing store: sqlite

The registry persists to a sqlite database under the CP-owned data subdirectory:

- Host: `<DataDir>/controlplane/controlplane.db`
- Container: `consts.CPControlPlaneDBPath` (bind-mounted RW into `consts.CPControlPlaneDir`)
- Driver: `modernc.org/sqlite` (pure Go — no cgo toolchain in the CP build pipeline)

The DB survives CP container recreation. Rows reload into the in-memory cache on `NewSQLite`. Lookup/Snapshot serve from the cache (hot path on every AgentPort RPC); Add and EvictByContainerID are write-through (disk first, then cache, so a process crash mid-write never produces a row in memory that does not survive restart).

## Schema

```sql
CREATE TABLE IF NOT EXISTS agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  registered_at  INTEGER NOT NULL,
  last_seen      INTEGER NOT NULL,
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX IF NOT EXISTS idx_name_project ON agents(project, agent_name);
```

`thumbprint_hex` and `container_id` are individually UNIQUE: a given cert can bind to exactly one container and a given container can host exactly one registration. The composite PK encodes the binding intent. `(project, agent_name)` is non-unique — Docker container name uniqueness handles the real conflict upstream; brief stale rows during eviction races are tolerated.

`last_seen` mirrors `registered_at` today; future per-agent RPCs will refresh it at their own boundary.

## Registry row ↔ extant container invariant

A row exists ⇔ the named container exists in docker (running OR stopped). Eviction triggers narrowed to:

- **`docker rm` (DeltaRemoved)** — steady-state, driven by the dockerevents subscription. Container is gone for good, row is orphaned.
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
    Add(entry Entry) error                                       // Persist + cache. Returns sqlite write errors.
    Lookup(thumbprint [sha256.Size]byte, cn string) (*Entry, error)  // CN-gated identity resolution (IdentityInterceptor).
    LookupByThumbprint(thumbprint [sha256.Size]byte) (*Entry, error) // No CN check; used by Register handler's existing-thumbprint REJECT.
    LookupByContainerID(containerID string) (*Entry, error)          // Used by AnnounceAgent's pre-reserve evict.
    EvictByContainerID(containerID string)
    Snapshot() []Entry
}

func NewRegistry(log *logger.Logger) Registry              // In-memory only — used by tests.
func NewSQLite(dbPath string, log *logger.Logger) (Registry, error)  // Production: sqlite-backed.
```

`Add` panics on invalid input (zero thumbprint, empty AgentName, empty ContainerID, zero RegisteredAt) — these are programming errors at the call site, not user input.

`Add` returns an error when the sqlite write fails (UNIQUE collision against a stale row that wasn't evicted in time, disk full, schema corruption). The Register handler maps this to `codes.Internal`, clawkerd exits non-zero, the container's restart policy or the user re-runs `clawker start`, AnnounceAgent re-evicts and re-reserves.

## Subscriber

`Subscribe(ctx, registry, informer, log)` subscribes the registry to the shared dockerevents informer; **destroy only** (DeltaRemoved) drives `EvictByContainerID`. Stop/die/kill are deliberately ignored — see "Registry row ↔ extant container invariant" above. Reused by both in-memory and sqlite-backed registries — the subscription only sees the Registry interface.

## Reaper

`Reap(ctx, registry, lister, log)` runs once at CP startup. The lister enumerates every `purpose=agent` container with `All: true` (running + stopped + exited) so a stopped container with a live row survives the sweep. Returns the count of evicted rows for the caller's startup log. A failed list is a transient docker daemon issue — caller logs the warning and proceeds; the dockerevents subscription catches up on the next destroy event.

## Mock

moq-generated `RegistryMock` under `mocks/`. Regenerate via `go generate ./...`.

## Imports

**Uses**: `internal/auth` (CanonicalAgentCN, MustProjectSlug, MustAgentName), `internal/logger`, `modernc.org/sqlite`, stdlib `crypto/sha256`, `database/sql`, `encoding/hex`.

**Used by**: `cmd/clawker-cp/main.go` (NewSQLite construction), `internal/controlplane/server.go` (AnnounceAgent + ListAgents), `internal/controlplane/agent` (Register handler + IdentityInterceptor).
