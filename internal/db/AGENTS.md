# CLI Database Package

Owns the process-wide CLI SQLite connection, schema migrations, and the domain
stores that use that connection.

## Package Boundary

`DB` is connection machinery only. It opens, migrates, and closes the CLI
database. Do not add table-specific methods to `DB`.

Each table has a separate store type. A command gets the process-wide `*DB`
from the Factory and constructs the store that it needs. Do not add a
table-specific Factory noun.

## Public API

```go
func Open(path string, log *logger.Logger) (*DB, error)
func (d *DB) Close() error

func NewSocketGrantStore(database *DB) SocketGrantStore
```

`SocketGrantStore` owns all socket grant operations. Consumers depend on this
interface. Its moq-generated test double is
`internal/db/mocks.SocketGrantStoreMock`.

The socket grant principal is the resolved harness component directory plus the
resolved host socket path. A stored row also pins the observed listener user ID
and group ID.

## Schema

The schema uses SQLite `PRAGMA user_version`. Migrations run in `Open` before
the connection is returned. Add migrations in version order. Do not change an
old migration after release.

The connection uses WAL mode, a busy timeout, and one open connection. This
configuration serializes CLI writers and prevents lock failures between short
CLI processes.

## Testing

Run:

```bash
go test ./internal/db/...
```

Tests use a database in `t.TempDir()`. They cover migration, grant replacement,
identity data, revocation, pruning, and concurrent writers.
