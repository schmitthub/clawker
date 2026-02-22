# Config Schema Refactor — Removed Fields

## Status: COMPLETE (2026-02-22)

## What Changed
Four fields were removed from the persisted config schema:

| Removed Field | Replacement |
|---------------|-------------|
| `Project.Name` | `project.ProjectManager.CurrentProject(ctx).Name()` — project identity lives in the registry, not config |
| `Project.Version` | Deleted entirely; image version labels use `build.Version` (binary version via ldflags) |
| `Project.DefaultImage` | `Project.Build.Image` (already existed in schema) |
| `Settings.DefaultImage` | `Project.Build.Image` via `cfg.SetProject`/`cfg.WriteProject` |

## DI Pattern
- **Command layer**: `ProjectManager func() (project.ProjectManager, error)` closure on Options structs, wired from `f.ProjectManager` in `NewCmd*`. Testable via `projectmocks.NewMockProjectManager()`.
- **Library layer** (`Builder`, `Runner`, etc.): receives resolved `projectName string`. No ProjectManager dependency.

## Key API Changes
- `docker.NewBuilder(cli, cfg, workDir)` → `docker.NewBuilder(cli, cfg, workDir, projectName string)`
- `client.ResolveImageWithSource(ctx)` → `client.ResolveImageWithSource(ctx, projectName string)`
- `client.ResolveImage(ctx)` → `client.ResolveImage(ctx, projectName string)`
- `docker.ImageSourceDefault` constant — deleted
- `client.ResolveDefaultImage()` — deleted
- `shared.persistDefaultImageSetting()` — deleted
- `RebuildMissingImageOpts.Cfg` field — deleted
- `loop/shared.Options.ProjectName string` — new field (replaces `ProjectCfg.Name`)

## Empty Project Handling
`ImageTag("")` → `"clawker:latest"`, `ContainerName("", agent)` → 2-segment `"clawker.agent"`, `ImageLabels("", ...)` omits project label.

## Commands Updated
attach, inspect, restart, start, run, create, image build, loop iterate, loop tasks — all now wire `ProjectManager` from Factory and resolve `projectName` early.