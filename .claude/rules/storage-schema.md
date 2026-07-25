---
description: Storage schema struct tag contract and field system
paths:
  - "internal/storage/**"
  - "internal/config/schema*"
  - "internal/config/defaults*"
---

# Storage Schema Contract

## Struct Tag Contract

Schema types use these struct tags as the single source of truth for field metadata. `NormalizeFields[T]()` reads them at runtime and produces a `FieldSet`.

| Tag | Purpose | Fallback | Example |
|-----|---------|----------|---------|
| `yaml:"name"` | Dotted YAML path key | Lowercased field name | `yaml:"default_mode"` |
| `label:"Display Name"` | Human-readable label for TUI/docs | YAML key | `label:"Default Mode"` |
| `desc:"Help text"` | Field description | Empty | `desc:"Workspace mounting mode"` |
| `default:"value"` | Default value (used by `GenerateDefaultsYAML`) | Empty | `default:"bind"` |
| `required:"true"` | Marks load-bearing fields that must have a value | `false` | `required:"true"` |
| `merge:"union"` | Merge strategy for slices/maps across layers: `"union"` = additive, `""` = last-wins | `""` (last-wins) | `merge:"union"` |

### Default Tag Value Formats

| Go Type | FieldKind | Format | Example |
|---------|-----------|--------|---------|
| `string` | KindText | Raw string | `default:"bind"` |
| `bool` | KindBool | `"true"` or `"false"` | `default:"false"` |
| `*bool` | KindBool | `"true"` or `"false"` | `default:"true"` |
| `int` / `int64` | KindInt | Decimal string | `default:"50"` |
| `[]string` | KindStringSlice | Comma-separated | `default:"git,curl,ripgrep"` |
| `map[string]string` | KindMap | Comma-separated `key=value` (split on first `=`; values may contain `=` but not `,`) | `default:"dev=run --rm -it @"` |
| `time.Duration` | KindDuration | Go duration string | `default:"30s"` |
| `time.Time` | KindTime | RFC3339Nano scalar (serialized via yaml.v3, not recursed) | (usually no default) |

## Key Functions

### `storage.NormalizeFields[T](v T, opts ...NormalizeOption) FieldSet`
Reflects over struct tags, maps Go types to `FieldKind`, returns `FieldSet`. Does NOT extract runtime values. Panics on unrecognized types unless a `KindFunc` claims them (see below).

### `storage.GenerateDefaultsYAML[T Schema]() string`
Walks struct tags (type-level, not value-level), collects fields with non-empty `default` tag, builds nested `map[string]any` with typed coercion (bools → Go bool, ints → Go int64, etc.), marshals to YAML. Output feeds `WithDefaults()`.

### `storage.WithDefaultsFromStruct[T Schema]() Option`
Convenience wrapper: `WithDefaults(GenerateDefaultsYAML[T]())`.

## Schema → Store Constraint

`Store[T Schema]` is compile-time enforced. All types stored in a `Store` must implement `Schema` (i.e., have `Fields() FieldSet`). This ensures every stored config type exposes field metadata.

## Extensible Kind System (`KindFunc`)

Storage classifies the shapes in the `FieldKind` table above, including the composite ones: `map[string]string` → `KindMap`, `[]T` where `T` is a struct → `KindStructSlice`, and `map[string]T` where `T` is a struct → `KindStructMap`. A **struct-valued map is native** — a schema field like `map[string]WorktreeEntry` needs nothing but `storage.NormalizeFields(r)`:

```go
func (r ProjectRegistry) Fields() storage.FieldSet {
    return storage.NormalizeFields(r)
}
```

`WithKindFunc` is for a shape the engine genuinely cannot classify — one that falls to `normalizeStruct`'s default branch and would otherwise panic (e.g. `map[string][]string`, `[]int`, or a named type whose underlying kind is none of the above). Domain-specific types must NOT be added to storage; the consumer registers the kind instead:

```go
// Consumer package defines its kind constant:
const KindTagSets storage.FieldKind = storage.KindLast + 1

// Consumer's Schema.Fields() implementation registers it:
func (s MySchema) Fields() storage.FieldSet {
    return storage.NormalizeFields(s, storage.WithKindFunc(func(ft reflect.Type) (storage.FieldKind, bool) {
        if ft == reflect.TypeOf(map[string][]string{}) {
            return KindTagSets, true
        }
        return 0, false // fall through → panic (forces explicit handling)
    }))
}
```

`KindLast` is the extension boundary. Consumer kinds use `storage.KindLast + 1`, `+ 2`, etc. When `normalizeStruct` encounters an unknown type, it tries the `KindFunc` before panicking. A `KindFunc` that returns a kind `<= KindLast` panics — consumer kinds must be strictly greater. StoreUI enforces read-only on consumer-defined kinds (`> KindLast`) in `fieldsToBrowserFields`.

## Enum-Shaped Fields (closed value sets)

A field whose value must come from a closed set gets a named type with a
validating `yaml.Unmarshaler` — the yaml-native mechanism, zero engine
involvement. Storage's strict decode IS a yaml.v3 `Decode`, so the unmarshaler
runs at both validation moments automatically: an invalid on-disk value fails
construction, and `Set` rejects it in the candidate decode (nothing staged).
Reference: `config.Mode` (`internal/config/consts.go`) backing
`workspace.default_mode`.

```go
func (m *Mode) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil { ... }
	parsed, err := ParseMode(s) // the enum gate
	...
}
```

Unknown KEYS stay tolerated (dropped/preserved by the store); the unmarshaler
gates only the VALUE of the declared field. Do not build engine-side enum
tags for this — the unmarshaler interface already is the validation seam.

## When Adding a New Config Field

1. Add the field to the struct in `schema.go` with `yaml`, `label`, and `desc` tags
2. If it needs a default, add `default:"value"` tag
3. If it's load-bearing, add `required:"true"` tag
4. If its value comes from a closed set, use a named type with a validating `UnmarshalYAML` (see "Enum-Shaped Fields")
5. If its type falls outside every shape storage classifies (struct-valued slices and maps are native — check the `FieldKind` list first), register a custom `FieldKind` via `KindFunc` in the schema's `Fields()` method — do not add domain types to storage
6. CI enforces non-empty `desc` via `TestProjectFields_AllFieldsHaveDescriptions` and `TestSettingsFields_AllFieldsHaveDescriptions`

## No Hardcoded YAML Templates

Default values live on struct tags, not in YAML string constants. `internal/config/defaults.go` contains firewall rules and constants, not YAML template strings. `clawker init` generates the project file by writing a preset-populated `storage.Store[Project]` via `store.WriteTo(configPath)`, not by string-manipulating a hardcoded template. Blank configs (e.g. `NewBlankConfig`) are populated via `GenerateDefaultsYAML[T]()` from the same struct tags.
