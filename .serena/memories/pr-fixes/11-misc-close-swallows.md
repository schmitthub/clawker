# Task 11 — misc close-error swallows: agents.go + clawkerd main.go

**Status**: complete
**Claimed by**: claude-opus-4.7
**Blocks**: —
**Blocked by**: 03 (agents.go gets restructured first)
**Parallel-safe**: yes

## Findings covered

- **S16** — `internal/cmd/controlplane/agents.go:120-124` — `_ = closer.Close()` for sqlite reader discards error.
- **S18** — `cmd/clawkerd/main.go:106` — `_ = log.Close()` for logger flush discards error.

## Decisions

- **S16**: Log via `opts.Logger()` at Debug. CLI exit-time; logger still alive.
- **S18**: Print error to stderr (the logger itself is what's closing — can't log via it).

## Affected files

| File | Change |
|------|--------|
| `internal/cmd/controlplane/agents.go` | Replace `_ = closer.Close()` (in the deferred sqlite close) with logged variant. |
| `cmd/clawkerd/main.go` | Replace `_ = log.Close()` at exit with stderr-fallback variant. |

## Implementation plan

1. **agents.go change** (depends on Task #3 having restructured the file):
   ```go
   defer func() {
       if closer == nil { return }
       if err := closer.Close(); err != nil {
           if logger, lerr := opts.Logger(); lerr == nil {
               logger.Debug().Err(err).Msg("agents: sqlite reader close failed")
           }
       }
   }()
   ```
   Logger access via `opts.Logger()` — note the double-error pattern (logger init can itself fail; if so, silently drop the close error since there's nowhere to surface it).

2. **clawkerd main.go change** at L106:
   ```go
   if err := log.Close(); err != nil {
       fmt.Fprintf(os.Stderr, "clawkerd: logger close failed: %v\n", err)
   }
   ```
   Stderr-only because the logger is what's being closed; the next line of zerolog output may be lost.

## Test requirements

- These are tiny defensive logging changes. No new tests required.
- If you want belt-and-suspenders: a unit test that confirms agents.go's deferred close calls Logger on close-failure (use a fake closer that returns an error). Optional.

## Verification

```bash
go build ./...
go vet ./internal/cmd/controlplane/... ./cmd/clawkerd/...
go test ./internal/cmd/controlplane/... -race -v
make test
```

## Dependencies

- **Task #3**: agents.go restructure must complete first (the line numbers and the surrounding code shift with Task #3).

## Risks / gotchas

- **Don't add a logger import to clawkerd/main.go just for this** — the file already imports the logger; the err path falls back to stderr because the logger is closing. If the file structure changes such that `log.Close()` happens BEFORE the logger is set up to close, the order is wrong; preserve current ordering.
- **`opts.Logger()` can itself error** in agents.go path. If it does, you have no logger to surface the close error — silently drop is acceptable here (the original code dropped the close error entirely; this change at minimum tries to log).
- **Be aware Task #3 may rename or restructure agents.go significantly** — if the deferred close pattern moves, find the new equivalent and apply this fix there.

## Reference reading

- `internal/cmd/controlplane/agents.go:120-124` (current — line numbers will shift after Task #3)
- `cmd/clawkerd/main.go:106` (current)
- Task #3 file (changes the agents.go structure first)

## Resolution

- Commit SHA: 3d26f9fe
- Notes:
  - `agents.go`: deferred sqlite reader close now logs `Debug` via the already-resolved `log` rather than dropping the error.
  - `clawkerd/main.go`: `log.Close()` failure now writes to `os.Stderr` since the logger itself is what's closing.
  - No new tests — both are tiny defensive logging changes.
