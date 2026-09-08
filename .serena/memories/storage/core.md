# YAML storage

- `internal/storage.Store[T]` owns discovery, YAML-node merge, validation, file locking, and atomic writes.
- Each caller package owns its schema, interface, constructors, migration definitions, and generated mocks.
- The constructor loads the store. There is no separate read/init/refresh phase, snapshot getter, closure mutator, or transaction wrapper.
- Public operations include `Keys`, `storage.Get[V]`, `Set`, `Remove`, `Write`, `WriteTo`, and `WriteFieldTo`.
- Read/write keys are explicit segments. Dotted keys in provenance display are output only.
- The merged YAML node tree is the in-memory source. `Get` decodes the requested subtree.
- `Set` validates a candidate tree before committing it in memory. File writes are a separate step.
- Writes apply changed fields to the destination file's own re-read node tree. Do not copy another layer's comments or unrelated values into that file.
- Migrations run per layer before merge and validation. Domain packages define the migrations; the engine controls execution.
- Defaults and string seeds are not dirty after construction. `MarkSeedForWrite` explicitly enables seed persistence.
- `NewFromString` without path options is an in-memory store. `Write` fails without a destination; use a real isolated store for persistence tests.
- Do not generalize the YAML engine rules to the separate SQLite agent registry.

## References

- Engine API: `internal/storage/AGENTS.md`.
- Package construction: Store-backed package contract section in `internal/storage/AGENTS.md`.
- Schema fields, tags, defaults, and validation: `internal/storage/AGENTS.md` → Struct Tag Contract.
- For config and project ownership: `mem:config/core`.
- Prior rewrite records: `mem:history/storage-redesign/status`. Their phase state is historical.
