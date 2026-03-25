# Copilot PR #191 Review Findings

Validated by multi-agent consensus (2 agents per finding). Branch: `feat/field-descriptions`.

## Valid Findings (need fixes)

### Code Bugs

- [x] **#2 offerUserDefault no-op Set → Write silently fails** — RESOLVED: deleted `offerUserDefault` entirely. Feature will return properly in init overhaul PR.

- [x] **#4 Write() doesn't rebuild tree/provenance after refreshLayers** — RESOLVED: Write() now calls remerge() after refreshLayers(), plus injectNewLayers() for newly created files. Removed redundant Refresh() calls from storeui.

- [x] **#5 Write() routes new map entry dirty paths to wrong layer** — RESOLVED: Root cause was deeper than routing — the tree engine couldn't distinguish `map[string]string` fields from struct nesting. Fix: (1) evolved `tagRegistry` to carry `FieldKind` as schema boundary, (2) `mergeTrees` now checks registry — opaque maps get tag-driven merge (union or last-wins) instead of implicit key-by-key, (3) `diffTreePaths` treats opaque maps as leaves (emits `"env"` not `"env.FOO"`), (4) `Write()` uses delete-then-set for opaque maps to get replace semantics. Also added `merge:"union"` to `labels` field; env maps default to overwrite.

- [x] **#10 Silent fallback in FieldBrowser when Editor returns non-FieldEditor** — RESOLVED: Silent fallback is intentional design (unresolvable fields degrade to browse-only). Fixed misleading "Programming error" comment to describe the intended behavior.

- [x] **#12 E2E migration tests don't assert command success** — `test/e2e/migration_test.go:49,88`
  - `h.Run("project", "info", ...)` return value discarded on both lines. If command errors, file content assertions could pass for wrong reasons.
  - Fix: capture result, `require.NoError(t, res.Err)` like the register step on line 42.

- [x] **#14 KVEditor allows duplicate keys → silent data loss** — RESOLVED (partial): Built generic editor validation system. (1) `renderValidationError()` shared helper for consistent error display across all editors, (2) added `Validator func(string) error` + `errMsg` + `Err()` to KVEditor/ListEditor/TextareaEditor via functional options (`WithKVValidator`, `WithListValidator`, `WithTextareaValidator`), (3) FieldBrowserModel now wires `BrowserField.Validator` to ALL editor types (was TextField-only), (4) `Err()` added to `FieldEditor` interface, (5) KV editor no longer blocks duplicate keys — editor shows merged state, duplicate key validation belongs at the write boundary (per-layer). **TODO**: Write-time overwrite confirmation UX: `Write()` sentinel errors → FieldBrowser "FOO exists, overwrite? y/n" → yes: edit existing entry, no: cancel save.

- [x] **#16 NormalizeFields doc says "panic" but impl silently skips** — RESOLVED: Made impl match doc — `normalizeStruct` now panics on unsupported field types (schema must be exhaustive). Also consolidated three duplicate type→kind mappings (`normalizeStruct`, `collectDefaults`/`fieldKindFor`, `walkType`/`classifyFieldKind`) into single canonical `NormalizeFields` path. `GenerateDefaultsYAML` and `buildTagRegistry` now consume `NormalizeFields` output instead of walking structs independently. Added `MergeTag()` to `Field` interface. `parseDefaultValue` panics on invalid int defaults. Net -135 lines.

- [x] **#17 KindMap classifies ALL reflect.Map, not just map[string]string** — RESOLVED: (1) `normalizeStruct` now checks `map[string]string` specifically for `KindMap`; all other map types try consumer-registered `KindFunc` before panicking. (2) Added extensible kind system: `KindLast` boundary constant, `KindFunc` type, `WithKindFunc` option on `NormalizeFields`. Consumers define domain kinds as `storage.KindLast + 1` and register via `KindFunc` — storage stays free of domain types. `KindFunc` returning storage-defined kinds (`<= KindLast`) panics. (3) `classifyAndFormat` falls back to `KindStructSlice` for unrecognized types (expected with `KindFunc`; `enrichWithSchema` overwrites kind from schema). (4) `fieldKindToBrowserKind` default → `BrowserStructSlice`; `fieldsToBrowserFields` forces `ReadOnly=true` for consumer-defined kinds (`> KindLast`). (5) `buildTagRegistry` and `GenerateDefaultsYAML` route through `Fields()` so consumer `KindFunc` classifiers are applied.

### Documentation Drift

- [x] **#7 storage/CLAUDE.md out of date** — RESOLVED: Fixed `DeleteKey` → `Delete`, `Write(filename ...string)` → `Write(opts ...WriteOption)` with `ToPath`/`ToLayer`. Earlier KindComplex fixes landed in #17.

- [x] **#8 .claude/rules/storeui.md out of date** — RESOLVED: Replaced `writeFieldToFile`/`typedYAMLValue` refs with current `store.Write(storage.ToPath(...))` flow. Fixed `store.DeleteKey` → `store.Delete`, `store.WriteTo` → `store.Write(storage.ToPath(...))`. Also updated `internal/storeui/CLAUDE.md` with same fixes. Earlier KindComplex fix landed in #17.


## Disagreements (user decides)

- [x] **#3 RenderLeftLabeledDivider off-by-one** — RESOLVED: Changed guard from `labelLen+2` to `labelLen+1`. A 1-char trailing rule is fine; dropping the label entirely was worse. Added `TestRenderLeftLabeledDivider_ExactFit` boundary test.

- [x] **#6 diffTreePaths uses fmt.Sprintf("%v") for comparison** — RESOLVED: Replaced `fmt.Sprintf("%v")` leaf comparison with `reflect.DeepEqual`. Map-vs-map case was already fixed in #5; this hardens the scalar/slice leaf path against future regressions (e.g., `[]any` containing maps).

- [x] **#15 parseDefaultValue treats bool typos as false** — RESOLVED: Added panic on invalid bool defaults (must be exactly `"true"` or `"false"`). Consistent with KindInt which already panics. Added panic test case.

## False Positives (no action needed)

- **#1 GenerateDefaultsYAML pointer type panic** — False positive. `reflect.TypeOf` on a typed nil pointer returns the type, not nil. Existing pointer guard handles it.
- **#13 structToMap skips empty strings** — False positive. Intentional, well-documented, regression-tested design decision. Empty string = "not set" at struct field level.
