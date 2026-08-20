# Sockets Command Package

Owns the `clawker sockets` command group for stored host socket bridge
approvals and denials.

## Commands

- `sockets list` lists stored decisions.
- `sockets info <id>` shows one full decision.
- `sockets revoke` deletes one decision, all decisions for a harness name, or
  all decisions.
- `sockets prune` deletes decisions that current harness declarations do not
  use.

There is no command that creates an approval. Approval occurs only during
`run`, `start`, or `restart`, when clawker can inspect the socket listener.

## Dependency Shape

The Factory has one process-wide database noun:

```go
DB func() (*db.DB, error)
```

`NewCmdSockets` composes a `socketGrantStoreFunc` over `f.DB()` and
`db.NewSocketGrantStore`. Each Options struct keeps this store-interface
closure. Tests inject `db/mocks.SocketGrantStoreMock`; they do not construct the
production Factory.

Do not add `SocketGrants` or another table-specific noun to the Factory.

## Output

`list` and `info` support the shared format flags, including JSON, templates,
and quiet output. Human output uses `tui.Table` or `tui.RenderDetails`.

Revocation and pruning tell the user that a running bridge stays active until
the container stops or restarts. `--all` requires confirmation unless `--yes`
is set. A non-interactive call must use `--yes`.

## Testing

Run:

```bash
go test ./internal/cmd/sockets/...
```

Tests construct a small Factory, inject the moq store mock, and verify command
selection, output formats, completion, confirmation, and prune behavior.
