# UID/GID Constants — Tracking Issue

## Status: Not Started

## Problem
Container UID/GID 1001 is hardcoded in multiple packages with "Must match bundler.DefaultUID" comments.
The canonical source is `internal/bundler/dockerfile.go` (`DefaultUID = 1001`, `DefaultGID = 1001`).

## Locations

| File | Line(s) | Usage |
|------|---------|-------|
| `internal/bundler/dockerfile.go` | 93-94 | Canonical constants: `DefaultUID = 1001`, `DefaultGID = 1001` |
| `internal/bundler/dockerfile.go` | 253-254 | Used in Dockerfile generation |
| `internal/containerfs/containerfs.go` | ~168-169 | `PrepareOnboardingTar` tar headers |
| `internal/docker/volume.go` | ~67 | `chown -R 1001:1001` command |
| `internal/docker/volume.go` | ~199-200 | `createTarArchive` tar headers |
| `test/harness/client.go` | ~461 | `adduser -u 1001` in test Dockerfile |

## Fix Suggestion

Option A: Move constants from `bundler` to a shared leaf package (e.g., `internal/config/defaults.go` or new `internal/containeruser/` package).

Option B: Export from bundler and import — but bundler is leaf (no docker import), so docker/volume.go importing bundler would need import cycle check.

Option C: Add to `internal/config` as `ContainerUID`/`ContainerGID` constants — config is already imported by containerfs and docker.

**Recommended:** Option C — `config.ContainerUID` and `config.ContainerGID` in `internal/config/defaults.go`.
Then update all locations to reference the constants.
