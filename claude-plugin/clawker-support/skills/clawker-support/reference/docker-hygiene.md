# Docker Hygiene

Reference for diagnosing and resolving Docker disk space issues in clawker
environments. Triggered when users report build failures, disk space warnings,
or ask about cleaning up Docker resources.

## Symptoms

Match on these error strings — users will often paste them verbatim:

**BuildKit (default build engine since Docker 20.10):**
- `ERROR: failed to solve: failed to read dockerfile: failed to create temp dir: mkdir /var/lib/docker/tmp/buildkit-mount...: no space left on device`
- `ERROR: failed to solve: failed to prepare ...: no space left on device`
- `failed to compute cache key: no space left on device`
- BuildKit may also **hang indefinitely** with no error message at all when disk is full

**Legacy Docker build engine:**
- `Error response from daemon: mkdir /var/lib/docker/tmp/docker-builder...: no space left on device`

**Image pull / layer registration:**
- `failed to register layer: Error processing tar file(exit status 1): write ... : no space left on device`
- `Error response from daemon: ... no space left on device` during `docker pull`

**General Docker operations:**
- `no space left on device` on any container create, start, or volume operation
- Docker Desktop showing high "Disk image size" in Settings > Resources

**Less obvious signs (no explicit error):**
- `clawker build` failing with no clear config issue
- Builds that previously worked now timing out or hanging
- User asking "how do I free up Docker space" or similar

## Diagnosis

Check Docker disk usage first:

```bash
docker system df
docker system df -v   # detailed per-resource breakdown
```

The main consumers in a clawker environment:

| Resource | Typical cause | Command to check |
| --- | --- | --- |
| Images | Repeated `clawker build` across projects | `clawker image list` / `docker images` |
| Build cache | BuildKit intermediate layers | `docker system df` (build cache row) |
| Volumes | Config/history volumes per project+agent | `clawker volume list` |
| Stopped containers | Containers not removed after stopping | `clawker container list --all` |

## Cleanup — safest to most aggressive

**Always start with clawker-scoped cleanup** — these only affect clawker-labeled
resources and won't touch other Docker workloads:

1. `clawker image prune` — remove unused clawker images
2. `clawker volume list` + `clawker volume remove <name>` — targeted volume cleanup
3. `clawker rm <name>` — remove specific stopped containers

**Avoid `clawker volume prune`** unless the user explicitly wants a full reset —
it removes all unattached clawker volumes including config and shell history.

**If that's not enough, escalate to Docker-wide commands:**

4. `docker builder prune` — clear BuildKit cache (often the biggest win)
5. `docker system prune` — remove all unused images, containers, networks
6. `docker system prune --volumes` — also removes unused volumes (**data loss
   risk** — removes ALL unused volumes, not just clawker's)
7. `docker builder prune --all` — nuclear option, removes all BuildKit cache

**Warn the user before running Docker-wide commands** — they affect all Docker
workloads, not just clawker.

## After cleanup

After freeing space, the user should rebuild their project image:

```bash
clawker build
```

The first rebuild after a cache prune will be slower since BuildKit needs to
recreate cached layers.

## Prevention

There's no automatic cleanup built into clawker. Users should periodically
check `docker system df` and prune when usage is high. Point them to
`https://docs.clawker.dev/docker-hygiene` for the full guide.

## Docker Desktop storage cap

Docker Desktop has a configurable virtual disk size limit (Settings > Resources).
When this cap is reached, all Docker operations fail — not just clawker. The
fix is either pruning resources or increasing the cap in Docker Desktop settings.
