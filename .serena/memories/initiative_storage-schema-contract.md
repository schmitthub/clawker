# Storage Schema Contract Initiative

**Branch:** `feat/storage-schema-contract`
**Parent memory:** `brainstorm_config-field-descriptions`
**PRD Reference:** GitHub Issue #178

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Storage contract foundation — interfaces, types, normalizer | `complete` | opus-4.6 |
| Task 2: Config schema annotations — struct tags + Schema impl on Project and Settings | `complete` | opus-4.6 |
| Task 3: Remaining types — Schema impl on EgressRulesFile and ProjectRegistry | `complete` | opus-4.6 |
| Task 4: Store constraint + storeui refactor — `Store[T Schema]`, consume `Fields()`, slim adapters | `complete` | opus-4.6 |
| Task 5: Enforcement, documentation, brainstorm wrap-up | `complete` | opus-4.6 |
| Task 6: Eliminate default templates — struct-tag-driven defaults | `complete` | opus-4.6 |

## Key Learnings

(Agents append here as they complete tasks)

- **Task 1**: Reuse existing `yamlTagName` from `merge.go` instead of duplicating. `NewFieldSet` should panic on duplicate paths (matching `ApplyOverrides` convention). `NormalizeFields` needs a nil guard before `reflect.TypeOf`. `FieldKind.String()` is valuable for test failure readability. `All()` should return a defensive copy. `NewField` should validate non-empty path. `yaml:",omitempty"` edge case is a known regression vector — always test it.
- **Task 2**: Do NOT add desc/label tags on struct-type fields — normalizer recurses into them, parent-level tags are silently discarded. DockerInstructions.Env is injected at runtime, not baked into image. Use "allow" not "whitelist" terminology. Parity tests should compare paths not just counts. Lost TODO comments must be restored when rewriting struct definitions.
- **Task 3**: Don't write enforcement tests for single-field wrapper structs (EgressRulesFile, ProjectRegistry) — the "all fields have descriptions" pattern only has value for schemas with many fields where forgetting one is a realistic risk.
- **Task 6**: `GenerateDefaultsYAML[T]()` walks struct type (not values) to collect `default` tags. Type coercion is critical — YAML must produce typed values (bool not string, int not string). Bridge tests comparing legacy YAML constants against generated output proved exact parity before deletion. `NewProjectWithDefaults()` uses `NewFromString` round-trip which is simpler than direct reflection. `scaffoldProjectConfig` now adds firewall domains/rules explicitly instead of relying on template content. `yaml.Marshal` doesn't quote strings — test assertions must match unquoted format. Registry store doesn't need `WithDefaults` at all — nil `[]ProjectEntry` works fine.