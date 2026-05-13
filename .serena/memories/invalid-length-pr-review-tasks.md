# invalid-length-pr-review-tasks

Tasks on branch `fix/invalid-name-len`.

---

## Task 1: AgentFullNameFromCert three-state return

**Files:**
- `internal/auth/agent_cert.go` — `AgentFullNameFromCert`, `sanTailFromCert`
- `internal/controlplane/agent/handler.go` — `peerIdentityFromContext`
- `internal/controlplane/agent/identity_interceptor.go` — stage 2a reject path

Change `AgentFullNameFromCert` return shape so the caller can distinguish:
1. No `urn:clawker:agent:` URI SAN present
2. SAN present but malformed (scheme parse error, empty tail)
3. SAN present and valid

Update the interceptor to emit `event=agent_identity_malformed_agent_san` for case 2 separately from `event=agent_identity_no_agent_san` for case 1.

---

## Task 2: Volume-name headroom test coverage for new purpose suffixes

**Files:**
- `internal/docker/names.go` — `purpose` suffixes
- `internal/docker/names_test.go` — `TestContainerName_HeadroomForMaxFields`

When `names.go` adds or lengthens a purpose suffix, extend the test to exercise the new longest suffix against `auth.MaxShortNameLen()` so the composed name overflow is caught at test time.

---

## Task 3: Cancel CP→clawkerd Session on registry evict

**Files:**
- `internal/controlplane/agent/dialer.go` — `runDial` loop
- `internal/controlplane/agent/start.go` — evict subscriber

Plumb a per-container cancel handle from the dialer into the registry-evict subscriber. On `EvictByContainerID`, cancel the in-flight Session stream synchronously rather than waiting for the next dialer reconnect to observe `outcomeContainerGone`.

---

## Task 4: Convert validateEntry panics to error returns

**Files:**
- `internal/controlplane/agent/registry.go` — `validateEntry` (FIXME comment present)
- `internal/controlplane/agent/registry.go` — in-memory `Add`
- `internal/controlplane/agent/registry_sqlite.go` — sqlite `Add`

Change `validateEntry` from `panic(...)` to `return error`. Update both `Add` implementations to propagate the error. Update tests in `registry_test.go` and `registry_sqlite_test.go` that assert panic semantics to assert error returns.

---

## Task 5: Recover from malformed registry row in Register handler

**Files:**
- `internal/controlplane/agent/registry_sqlite.go` — `scanEntry`, `LookupByContainerID`
- `internal/controlplane/agent/register_handler.go` — `Register`

Add a typed sentinel `ErrMalformedEntry` in `registry.go`. Return it from `scanEntry` when `auth.NewProjectSlug` / `auth.NewAgentName` re-validation fails. `LookupByContainerID` propagates the sentinel. `Register` checks `errors.Is(err, ErrMalformedEntry)`, calls `EvictByContainerID(containerID)`, then proceeds with the normal Register write path (idempotent).

---

## Task 6: Typed Less comparators on identity values

**Files:**
- `internal/auth/identity.go`
- `internal/controlplane/agent/registry.go` — in-memory Snapshot sort
- `internal/controlplane/agent/registry_sqlite.go` — sqlite Snapshot sort

Add `func (p ProjectSlug) Less(other ProjectSlug) bool` and `func (a AgentName) Less(other AgentName) bool` to `internal/auth/identity.go`. Replace `.String() < .String()` comparisons at every sort site under `internal/controlplane/agent/` (grep `\.String() <` for the full list).

---

## Task 7: Extract sortEntries helper

**Files:**
- `internal/controlplane/agent/registry.go`
- `internal/controlplane/agent/registry_sqlite.go`

Extract the duplicated Snapshot sort closure into one unexported helper `func sortEntries(entries []Entry)` in `registry.go`. Update both `Snapshot` implementations to call it.

---

## Task 8: Snapshot returns query error

**Files:**
- `internal/controlplane/agent/registry.go` — `Registry` interface, in-memory `Snapshot`
- `internal/controlplane/agent/registry_sqlite.go` — `Snapshot`
- `internal/controlplane/agent/start.go` — `reapOrphans`
- `internal/controlplane/server.go` — `ListAgents`
- `internal/controlplane/agent/registry_mock_test.go` (regen via `go generate ./...`)

Change `Snapshot()` from `[]Entry` to `([]Entry, error)`. Return a wrapped error on `db.Query` failure and on `rows.Err()` non-nil. Update `reapOrphans` to abort on error rather than treat an empty/truncated snapshot as authoritative. Update `ListAgents` to return the error as a gRPC error. Regenerate the moq mock.

---

## Task 9: Make migration 00002 one-way

**Files:**
- `internal/controlplane/agent/migrations/00002_drop_canonical_cn.sql`

Remove the `-- +goose Down` half of the migration. Add a SQL comment stating the migration is one-way. Match the policy in `migrateFromPreGoose` which treats registry state as regenerable.

---

## Verification

After each task:
```bash
make test GOFLAGS="-trimpath -buildvcs=false"
go build ./...
```

Inside a clawker container, do not run `go test ./...` (the e2e suite tears down the host CP).
