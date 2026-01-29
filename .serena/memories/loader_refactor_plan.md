# Config Architecture Refactor + Command Dependency Injection

## Branch: a/loader-refactor

## Overview
Three interrelated changes:
1. **Config architecture** — Three config files with clean separation (settings, registry, project config)
2. **Project resolution** — Registry-based lookup replaces directory walking; all resolution logic in `internal/config/`
3. **Command dependency injection** — Commands receive function references on options structs instead of `*Factory` directly

## Phase A: Config Infrastructure (internal/config/)
- Step 1: `internal/config/registry.go` — CREATE (ProjectEntry, ProjectRegistry, RegistryLoader)
- Step 2: `internal/config/registry_test.go` — CREATE
- Step 3: `internal/config/resolver.go` — CREATE (Resolution, Resolver)
- Step 4: `internal/config/resolver_test.go` — CREATE
- Step 5: `internal/config/loader.go` — MODIFY (functional options, LoaderOption, WithUserDefaults, WithProjectKey)
- Step 6: `internal/config/loader_test.go` — MODIFY
- Step 7: `internal/config/schema.go` — MODIFY (Project yaml:"-" mapstructure:"-")
- Step 8: `internal/config/settings.go` — MODIFY (remove Projects, ProjectDefaults)
- Step 9: `internal/config/settings_loader.go` — MODIFY (remove AddProject, RemoveProject, IsProjectRegistered)
- Step 10: `internal/config/validator.go` — MODIFY (remove validateProject)
- Step 11: `internal/config/defaults.go` — MODIFY (remove project from DefaultConfigYAML, update settings, add DefaultRegistryYAML)
- Step 12: `internal/config/home.go` — MODIFY (add RegistryFileName)

## Phase B: Factory + cmdutil Updates
- Step 13: `internal/cmdutil/factory.go` — MODIFY (registry/resolution lazy loading, new methods)
- Step 14: `internal/cmdutil/project.go` — MODIFY (remove dir walking, use Resolution)
- Step 15: `internal/cmdutil/resolve.go` — MODIFY (use function references instead of *Factory)

## Phase C: Docker Naming + Labels
- Step 16: `internal/docker/names.go` — MODIFY (handle empty project in ContainerName, VolumeName, ImageTag)
- Step 17: `internal/docker/labels.go` — MODIFY (omit com.clawker.project label when project empty)

## Phase D: Command Dependency Injection
- Step 18: Update ALL command options structs and NewCmd/run functions (~40 files)
- Step 19: `internal/cmd/project/init/init.go` — Rework init flow (prompt, slugify, register, optional clawker.yaml)

## Phase E: Tests
- Step 20: `internal/cmdutil/project_test.go` — MODIFY
- Step 21: `internal/config/settings_loader_test.go` — MODIFY
- Step 22: `internal/testutil/harness.go`, `config_builder.go` — MODIFY
- Step 23: ALL command test files — MODIFY (function references instead of Factory)
- Step 24: `internal/cmd/root/root.go`, `root_test.go` — MODIFY

## Status Tracking
- Phase A: COMPLETE — registry.go, resolver.go, loader.go, schema.go, settings.go, settings_loader.go, validator.go, defaults.go all updated
- Phase B: COMPLETE — Factory lazy-loads registry + resolution; cmdutil updated
- Phase C: COMPLETE — empty project → 2-segment orphan names, labels omit LabelProject when empty
- Phase D: COMPLETE — all command options structs use function references; project register command added
- Phase E: COMPLETE — all unit + integration tests pass
- Documentation: COMPLETE — CLAUDE.md, ARCHITECTURE.md, DESIGN.md, CLI-VERBS.md, package CLAUDE.md files updated
