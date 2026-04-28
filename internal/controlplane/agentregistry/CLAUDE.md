# agentregistry Package

Persisted record of which (cert thumbprint, container_id) pairs have completed `AgentService.Register`. Source of truth for "is this agent registered" — consumed by `agent.IdentityInterceptor` (every non-Register AgentPort RPC), `AdminService.AnnounceAgent` (evict-stale-row pre-flight), `AdminService.ListAgents`, and the dockerevents subscription that evicts on container die.

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

## Registry row ↔ running container invariant

A row exists ⇔ the named container is currently registered. Every container start (run + start) regenerates the cert at AnnounceAgent, so `AdminService.AnnounceAgent` evicts any prior row for the same container_id BEFORE reserving a new slot. The dockerevents subscription evicts on container die. Net effect: registry rows track running, healthy registrations only.

The CP-restart edge case (container restarted while CP was down → orphan row) is accepted; container restart heals it via the announce-time evict.

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

`Subscribe(ctx, registry, informer, log)` subscribes the registry to the shared dockerevents informer; container die/destroy events drive `EvictByContainerID`. Reused by both in-memory and sqlite-backed registries — the subscription only sees the Registry interface.

## Mock

moq-generated `RegistryMock` under `mocks/`. Regenerate via `go generate ./...`.

## Imports

**Uses**: `internal/auth` (CanonicalAgentCN, MustProjectSlug, MustAgentName), `internal/logger`, `modernc.org/sqlite`, stdlib `crypto/sha256`, `database/sql`, `encoding/hex`.

**Used by**: `cmd/clawker-cp/main.go` (NewSQLite construction), `internal/controlplane/server.go` (AnnounceAgent + ListAgents), `internal/controlplane/agent` (Register handler + IdentityInterceptor).
