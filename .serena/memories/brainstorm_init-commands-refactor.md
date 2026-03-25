# Brainstorm: Init Commands Grand Refactor

> **Status:** Completed
> **Created:** 2026-03-25
> **Last Updated:** 2026-03-25 00:06

## Problem / Topic
Clawker's init commands (`clawker init`, `clawker project init`, `clawker monitor init`) are not user-friendly. They need to become hand-holding walkthroughs that guide users step-by-step through the minimum viable settings. They should also offer pre-baked starter configs. The refactor leverages the existing storeui/wizard TUI infrastructure and may create new ones.

## Current State (As-Is)
- `clawker init`: 3-field wizard (build image? → flavor → confirm) → saves settings.yaml → optional base image build → optional "Customize settings?" storeui browser
- `clawker project init`: 4-field wizard (overwrite? → name → flavor → workspace mode) → creates .clawker.yaml + .clawkerignore → registers project → optional "Customize configuration?" storeui browser  
- `clawker monitor init`: No interactive mode at all — just scaffolds template files to data dir
- All three are disconnected experiences with no unified onboarding flow
- Wizard steps are minimal — they don't explain what things mean or why they matter
- No starter configs / templates / presets
- Post-wizard "Customize?" prompt dumps user into full field browser with no guidance

## Open Items / Questions
- Exact wizard field list for custom path — which complex fields get full editors vs simplified defaults?
- What explanatory text / descriptions accompany each wizard step?
- Non-interactive mode (`--yes`) behavior: which preset? default to Bare?
- Re-run confirmation UX details (what does the warning say?)


## Decisions Made
- `clawker init` becomes an alias for `clawker project init` (not deleted — graceful redirect)
- User-level settings bootstrap (settings.yaml + share dir) happens silently on first `project init` if missing
- Presets generate full clawker.yaml output, implementation is drift-proof: `GenerateDefaultsYAML[T]()` base + preset partial overlay merged via store machinery
- Build the guided flow as a new consumer of generic storeui/tui infrastructure (wizard, fields, stepper)
- `monitor init` stays separate (different concern, different lifecycle)
- Everything is store-backed end to end — presets bulk-populate Store[Project], custom path fills via wizard fields, both write via store.Write()
- Project name must be lowercase always — uppercase causes Docker BuildKit errors
- Presets by language: Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare, Build from scratch — 10 options
- Each language preset includes sensible firewall rules for that ecosystem's package registries
- Flow after preset picker has two branches:
  - Language/Bare selected → "Save and get started" / "Customize this preset" (2 options)
  - Build from scratch → straight to wizard with generic defaults
- "Build from scratch" is just Bare preset with auto-Customize (same code path, different label + skips "Save and get started" option)
- All presets including Bare run through the same two options: "Save and get started" / "Customize" — Build from scratch just auto-selects Customize on Bare
- Wizard fields (custom path): image, packages, root commands, user commands, injections, envs, firewall enabled, firewall rules, security — all pre-populated with sensible defaults, user accepts or modifies
- Firewall rules are a must-have in the wizard; injections TBD but likely included
- Final message always: "To customize further: clawker project edit"
- Kill the base image build step from init — confusing, forcing. User runs `clawker build` separately
- Re-running init on existing project: overwrite with warning + confirmation
- Preset definitions: Go string constants fed to `storage.NewFromString[Project](presetYAML)` — schema defaults fill gaps, unit tests catch regressions by constructing each string
- `config.NewFromString` and `storage.NewFromString[T]` are production constructors, not test infrastructure
- `config.NewFromString(presetYAML, settingsDefaultsYAML)` creates both stores in one call — first-run bootstrap writes both settings.yaml and clawker.yaml in one flow


## Conclusions / Insights
- Wizard and storeui field browser are two *presentation modes* for the same field metadata — not separate systems
- Both consume store-backed fields (WalkFields → Field), both write back via SetFieldValue → store.Set → store.Write
- A `storeui.Wizard[T]` function alongside `storeui.Edit[T]` could provide step-by-step guided editing for any Store[T], reusable beyond init
- Preset vs Custom is a spectrum, not a fork: preset = language-specific pre-populated values (no wizard), custom = generic pre-populated values (wizard walkthrough to review/modify)
- "Hand holding" means every wizard field is pre-populated with sensible defaults/examples; user accepts (Enter) or modifies — never blank fields
- Presets are by language: Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare, Custom — 10 options total


## Gotchas / Risks
- Project config (`Store[config.Project]`) and project registry (`Store[config.ProjectRegistry]`) are separate store backends in separate domains — init orchestrates both via command layer (Factory provides both dependencies)
- Preset picker is a meta-choice (determines wizard field list + defaults), not a store-backed field — needs to happen before the store-backed wizard


## Unknowns
- (none remaining)

## Next Steps
- All 4 tasks complete. Initiative finished 2026-03-25.
