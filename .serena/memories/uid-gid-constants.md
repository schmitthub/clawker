# UID/GID Constants — Tracking Issue

## Status: Completed

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

## Resolution

Constants `config.ContainerUID` and `config.ContainerGID` added to `internal/config/identity.go`.
All locations updated to reference these constants. `bundler.DefaultUID`/`DefaultGID` now alias them.
TODO comments removed.