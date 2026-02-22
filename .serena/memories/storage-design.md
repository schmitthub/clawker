# Storage Package Design (`internal/storage`)

> **Status:** Implementation complete. All 28 tests pass. Documentation complete.
> **Last Updated:** 2026-02-22
> **Branch:** `refactor/configapocalypse`
> **Related:** See `config-replacement-architecture` memory for the parent architecture.
> **Docs:** `internal/storage/CLAUDE.md` (full API reference), `.claude/docs/ARCHITECTURE.md` (triad diagram), `.claude/docs/DESIGN.md` §2.4

## Summary

Generic layered YAML store engine. Zero internal imports (leaf package). Both `internal/config` and `internal/project` compose a `Store[T]` with their own schema types. Replaces Viper.

## Node Tree Architecture

The node tree (`map[string]any`) is the merge engine and persistence layer. The typed struct `*T` is a deserialized view — the read/write API. This solves the `omitempty` problem (YAML marshaling drops zero-value fields).

```
Load:   file → map[string]any ─┐
                                ├→ merge maps → deserialize → *T
        string → map[string]any ─┘

Set:    *T (mutated) → structToMap → merge into tree → mark dirty

Write:  tree → route by provenance → per-file atomic write
```

## Package Files

| File | Purpose |
|------|---------|
| `errors.go` | Package doc, `ErrNotInProject`, `ErrRegistryNotFound` |
| `store.go` | `Store[T]`, `NewStore`, `NewFromString`, `Get`, `Set`, `Write`, `Layers` |
| `options.go` | `Option`, `Migration`, all `With*` constructors |
| `discover.go` | Walk-up + explicit path discovery, dual placement |
| `load.go` | Per-file YAML load, migration runner, `unmarshal[T]`, `layer` struct |
| `merge.go` | N-way map fold, `tagRegistry`, `mergeTrees`, `provenance` |
| `write.go` | `structToMap`, provenance-based routing, atomic I/O, flock |
| `resolver.go` | XDG dir resolution (`configDir`, `dataDir`, `stateDir`, `cacheDir`) |
| `storage_test.go` | 28 tests: load, merge, write, provenance, discovery, migrations |

## Key Design Decisions

- **Node tree as persistence layer** — `map[string]any` is canonical state. Struct `*T` is just a view.
- **`structToMap` ignores `omitempty`** — Reflection-based serializer. Nil = not set, zero = explicitly set.
- **`mergeIntoTree` preserves unknown keys** — Raw YAML content round-trips through Set without data loss.
- **Non-generic `layer`** — Holds `map[string]any`, not `*T`. Generic parameter only on `Store[T]` and helper functions.
- **`tagRegistry`** — Built once from `T`'s struct type at construction. Maps dotted paths to merge tags.
- **Map-based merge** — Absent keys = not iterated (no zero-value ambiguity). No `IsZero()` checks needed.

## Public API

```go
func NewStore[T any](opts ...Option) (*Store[T], error)
func NewFromString[T any](raw string) (*Store[T], error)

func (s *Store[T]) Get() *T
func (s *Store[T]) Set(fn func(*T))
func (s *Store[T]) Write(filename ...string) error
func (s *Store[T]) Layers() []LayerInfo
```

Options: `WithFilenames`, `WithDefaults`, `WithWalkUp`, `WithConfigDir`, `WithDataDir`, `WithStateDir`, `WithCacheDir`, `WithPaths`, `WithMigrations`, `WithLock`.

## Next Steps

- [ ] Migrate `internal/config` to compose `Store[ConfigFile]` + `Store[SettingsFile]`
- [ ] Migrate `internal/project` to compose `Store[Registry]`
- [ ] Update consumer mock APIs (`config/mocks`, `project/mocks`)
- [ ] Remove Viper dependency
