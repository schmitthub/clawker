# Storage Schema Contract

**Applies to**: `internal/storage/field*`, `internal/storage/defaults*`, `internal/config/schema*`, `internal/config/defaults*`

## Struct Tag Contract

Schema types use these struct tags as the single source of truth for field metadata. `NormalizeFields[T]()` reads them at runtime and produces a `FieldSet`.

| Tag | Purpose | Fallback | Example |
|-----|---------|----------|---------|
| `yaml:"name"` | Dotted YAML path key | Lowercased field name | `yaml:"default_mode"` |
| `label:"Display Name"` | Human-readable label for TUI/docs | YAML key | `label:"Default Mode"` |
| `desc:"Help text"` | Field description | Empty | `desc:"Workspace mounting mode"` |
| `default:"value"` | Default value (used by `GenerateDefaultsYAML`) | Empty | `default:"bind"` |
| `required:"true"` | Marks load-bearing fields that must have a value | `false` | `required:"true"` |

### Default Tag Value Formats

| Go Type | FieldKind | Format | Example |
|---------|-----------|--------|---------|
| `string` | KindText | Raw string | `default:"bind"` |
| `bool` | KindBool | `"true"` or `"false"` | `default:"false"` |
| `*bool` | KindBool | `"true"` or `"false"` | `default:"true"` |
| `int` / `int64` | KindInt | Decimal string | `default:"50"` |
| `[]string` | KindStringSlice | Comma-separated | `default:"git,curl,ripgrep"` |
| `time.Duration` | KindDuration | Go duration string | `default:"30s"` |

## Key Functions

### `storage.NormalizeFields[T](v T) FieldSet`
Reflects over struct tags, maps Go types to `FieldKind`, returns `FieldSet`. Does NOT extract runtime values.

### `storage.GenerateDefaultsYAML[T Schema]() string`
Walks struct tags (type-level, not value-level), collects fields with non-empty `default` tag, builds nested `map[string]any` with typed coercion (bools → Go bool, ints → Go int64, etc.), marshals to YAML. Output feeds `WithDefaults()`.

### `storage.WithDefaultsFromStruct[T Schema]() Option`
Convenience wrapper: `WithDefaults(GenerateDefaultsYAML[T]())`.

### `config.NewProjectWithDefaults() *Project`
Returns a `Project` populated with all default-tagged values. Used by `clawker project init` scaffolding.

## Schema → Store Constraint

`Store[T Schema]` is compile-time enforced. All types stored in a `Store` must implement `Schema` (i.e., have `Fields() FieldSet`). This ensures every stored config type exposes field metadata.

## When Adding a New Config Field

1. Add the field to the struct in `schema.go` with `yaml`, `label`, and `desc` tags
2. If it needs a default, add `default:"value"` tag
3. If it's load-bearing, add `required:"true"` tag
4. CI enforces non-empty `desc` via `TestProjectFields_AllFieldsHaveDescriptions` and `TestSettingsFields_AllFieldsHaveDescriptions`

## No Hardcoded YAML Templates

Default values live on struct tags, not in YAML string constants. `defaults.go` no longer contains template strings. The `clawker init` command generates YAML by marshaling a struct populated from defaults, not by string-manipulating a template.
